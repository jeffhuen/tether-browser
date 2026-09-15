package client

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"
)

// Proxy implements the Mode A origin-preserving forward proxy.
type Proxy struct {
	mu            sync.RWMutex
	listener      net.Listener
	server        *http.Server
	enrolled      map[string]string // target host (e.g. "localhost:3000") -> destination endpoint
	flushInterval time.Duration
	dialTimeout   time.Duration
}

// NewProxy creates a forward proxy instance.
func NewProxy(listenAddr string) *Proxy {
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	p := &Proxy{
		enrolled:      make(map[string]string),
		flushInterval: 10 * time.Millisecond,
		dialTimeout:   10 * time.Second,
	}
	p.server = &http.Server{
		Addr:    listenAddr,
		Handler: p,
	}
	return p
}

// Start begins listening on the configured address.
func (p *Proxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener != nil {
		return nil
	}

	ln, err := net.Listen("tcp", p.server.Addr)
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	p.listener = ln

	go func() {
		_ = p.server.Serve(ln)
	}()

	return nil
}

// Port returns the bound port number.
func (p *Proxy) Port() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.listener == nil {
		return 0
	}
	return p.listener.Addr().(*net.TCPAddr).Port
}

// Addr returns the listener address string.
func (p *Proxy) Addr() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

// Enroll maps a local host:port to a remote endpoint.
func (p *Proxy) Enroll(targetHost, remoteEndpoint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enrolled[targetHost] = remoteEndpoint
}

// Unenroll removes a target mapping.
func (p *Proxy) Unenroll(targetHost string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.enrolled, targetHost)
}

// ResolveTarget checks if a host is enrolled and returns its destination.
func (p *Proxy) ResolveTarget(host string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if remote, ok := p.enrolled[host]; ok {
		return remote, true
	}

	base := stripPort(host)
	if remote, ok := p.enrolled[base]; ok {
		return remote, true
	}

	return "", false
}

// Close terminates the proxy server and listener.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

// ServeHTTP routes CONNECT requests and plain HTTP requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		p.handleConnect(w, req)
		return
	}
	p.handlePlainHTTP(w, req)
}

// handleConnect splices TCP sockets for HTTPS and WebSockets.
func (p *Proxy) handleConnect(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	destAddr := host
	if remote, ok := p.ResolveTarget(host); ok {
		destAddr = remote
	}

	targetConn, err := net.DialTimeout("tcp", destAddr, p.dialTimeout)
	if err != nil {
		http.Error(w, fmt.Sprintf("proxy dial %s failed: %v", destAddr, err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("hijack failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	setTCPNoDelay(clientConn, true)
	setTCPNoDelay(targetConn, true)

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	spliceSockets(clientConn, targetConn)
}

// handlePlainHTTP proxies standard HTTP requests via streaming reverse proxy.
func (p *Proxy) handlePlainHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	remote, enrolled := p.ResolveTarget(host)

	rp := &httputil.ReverseProxy{
		FlushInterval: p.flushInterval,
		Director: func(outReq *http.Request) {
			outReq.URL.Scheme = "http"
			if enrolled {
				outReq.URL.Host = remote
			} else if outReq.URL.Host == "" {
				outReq.URL.Host = host
			}
			outReq.Host = host
		},
	}

	rp.ServeHTTP(w, req)
}

// setTCPNoDelay enables TCP_NODELAY if the connection is a TCP connection.
func setTCPNoDelay(conn net.Conn, noDelay bool) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(noDelay)
	}
}

// spliceSockets copies bidirectional data between two connections.
func spliceSockets(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c1, c2)
		if tc, ok := c1.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		} else {
			_ = c1.Close()
		}
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c2, c1)
		if tc, ok := c2.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		} else {
			_ = c2.Close()
		}
	}()

	wg.Wait()
}

func stripPort(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err == nil {
		return h
	}
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		return parts[0]
	}
	return host
}
