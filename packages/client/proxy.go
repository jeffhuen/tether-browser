package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnenrolledLoopback = errors.New("unenrolled loopback destination rejected")
)

// Proxy implements Mode A Origin-Preserving forward proxying with HTTP CONNECT tunneling.
type Proxy struct {
	listener    net.Listener
	server      *http.Server
	port        int
	dialTimeout time.Duration

	mu          sync.RWMutex
	routes      map[string]string // host:port -> remote host:port (e.g. "localhost:3000" -> "100.x.y.z:3000")
	activeConns map[net.Conn]struct{}
	closed      bool
}

// NewProxy creates a forward proxy ready to bind to a local port.
func NewProxy() *Proxy {
	return &Proxy{
		dialTimeout: 5 * time.Second,
		routes:      make(map[string]string),
		activeConns: make(map[net.Conn]struct{}),
	}
}

// Enroll adds an enrolled destination mapping (e.g. "localhost:3000" -> "127.0.0.1:3000" or remote address).
func (p *Proxy) Enroll(localHostPort, remoteHostPort string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[localHostPort] = remoteHostPort
}

// Unenroll removes an enrolled destination mapping.
func (p *Proxy) Unenroll(localHostPort string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, localHostPort)
}

// ResolveTarget checks if a host:port is enrolled.
func (p *Proxy) ResolveTarget(hostPort string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	target, ok := p.routes[hostPort]
	return target, ok
}

// ListenAndServe binds to 127.0.0.1 on the requested port (or 0 for ephemeral).
func (p *Proxy) ListenAndServe(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy listen on %s: %w", addr, err)
	}
	p.listener = ln
	p.port = ln.Addr().(*net.TCPAddr).Port

	p.server = &http.Server{
		Handler: p,
	}

	return p.server.Serve(ln)
}

// Port returns the listening port of the proxy.
func (p *Proxy) Port() int {
	return p.port
}

// Close gracefully terminates the proxy server and all active hijacked tunnel connections.
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.closed = true
	conns := make([]net.Conn, 0, len(p.activeConns))
	for c := range p.activeConns {
		conns = append(conns, c)
	}
	p.activeConns = make(map[net.Conn]struct{})
	p.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}

	if p.server != nil {
		return p.server.Close()
	}
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}

func (p *Proxy) trackConn(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.activeConns[c] = struct{}{}
	return true
}

func (p *Proxy) untrackConn(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.activeConns, c)
}

// ServeHTTP dispatches requests between CONNECT tunneling and plain HTTP proxying.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		p.handleConnect(w, req)
		return
	}
	p.handlePlainHTTP(w, req)
}

func resolveAndValidateDestination(hostPort string) (string, error) {
	host := hostPort
	port := ""
	if h, prt, err := net.SplitHostPort(hostPort); err == nil {
		host = h
		port = prt
	}

	// Normalize bracketed IPv6 hosts (e.g. "[::1]")
	hostClean := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")

	hLower := strings.ToLower(hostClean)
	if hLower == "localhost" || strings.HasSuffix(hLower, ".localhost") {
		return "", ErrUnenrolledLoopback
	}

	if ip := net.ParseIP(hostClean); ip != nil {
		if ip.IsLoopback() {
			return "", ErrUnenrolledLoopback
		}
		if port != "" {
			return net.JoinHostPort(ip.String(), port), nil
		}
		return ip.String(), nil
	}

	ips, err := net.LookupIP(hostClean)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", hostClean, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			return "", ErrUnenrolledLoopback
		}
	}

	// Use first validated IP to eliminate DNS rebinding
	validatedIP := ips[0].String()
	if port != "" {
		return net.JoinHostPort(validatedIP, port), nil
	}
	return validatedIP, nil
}

// handleConnect splices TCP sockets for HTTPS and WebSockets.
func (p *Proxy) handleConnect(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	destAddr, enrolled := p.ResolveTarget(host)
	if !enrolled {
		validated, err := resolveAndValidateDestination(host)
		if err != nil {
			if errors.Is(err, ErrUnenrolledLoopback) {
				http.Error(w, fmt.Sprintf("unenrolled loopback destination rejected: %s", host), http.StatusForbidden)
				return
			}
			http.Error(w, fmt.Sprintf("resolve %s failed: %v", host, err), http.StatusBadGateway)
			return
		}
		destAddr = validated
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

	clientConn, brw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("hijack failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	if !p.trackConn(clientConn) || !p.trackConn(targetConn) {
		return
	}
	defer p.untrackConn(clientConn)
	defer p.untrackConn(targetConn)

	setTCPNoDelay(clientConn, true)
	setTCPNoDelay(targetConn, true)

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	if brw != nil && brw.Reader.Buffered() > 0 {
		buffered := make([]byte, brw.Reader.Buffered())
		if _, err := io.ReadFull(brw.Reader, buffered); err == nil {
			if _, err := targetConn.Write(buffered); err != nil {
				return
			}
		}
	}

	spliceSockets(clientConn, targetConn)
}

// handlePlainHTTP proxies standard HTTP requests via streaming reverse proxy.
func (p *Proxy) handlePlainHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	destAddr, enrolled := p.ResolveTarget(host)
	if !enrolled {
		validated, err := resolveAndValidateDestination(host)
		if err != nil {
			if errors.Is(err, ErrUnenrolledLoopback) {
				http.Error(w, fmt.Sprintf("unenrolled loopback destination rejected: %s", host), http.StatusForbidden)
				return
			}
			http.Error(w, fmt.Sprintf("resolve %s failed: %v", host, err), http.StatusBadGateway)
			return
		}
		destAddr = validated
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://%s", destAddr))
	if err != nil {
		http.Error(w, "invalid destination", http.StatusBadRequest)
		return
	}

	rp := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = targetURL.Host
			r.Host = req.Host
			r.Header.Del("Proxy-Connection")
		},
		FlushInterval: 10 * time.Millisecond,
		ErrorHandler: func(rw http.ResponseWriter, r *http.Request, err error) {
			http.Error(rw, fmt.Sprintf("proxy error: %v", err), http.StatusBadGateway)
		},
	}

	rp.ServeHTTP(w, req)
}

func setTCPNoDelay(conn net.Conn, noDelay bool) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(noDelay)
	}
}

func spliceSockets(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}

	go copyHalf(c1, c2)
	go copyHalf(c2, c1)

	wg.Wait()
	_ = c1.Close()
	_ = c2.Close()
}
