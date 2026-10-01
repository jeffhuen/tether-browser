package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func (s *sshSession) serveProxy(ln net.Listener) {
	server := &http.Server{
		Handler:           http.HandlerFunc(s.handleSSHProxy),
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return
	}
	s.proxyServer = server
	s.mu.Unlock()
	// Server.Close before Serve is safe: Serve refuses a shut-down server.
	_ = server.Serve(ln)
}

func (s *sshSession) handleSSHProxy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ctx, socket, proxy := s.ctx, s.upstream, s.httpProxy
	connected := !s.closed && s.status.State == "connected"
	token, realm := s.proxyToken, s.proxyRealm
	s.mu.Unlock()

	auth := r.Header.Values("Proxy-Authorization")
	var credentials [72]byte // A 96-byte Base64 value decodes to at most 72 bytes.
	decoded := 0
	if len(auth) == 1 {
		scheme, encoded, ok := strings.Cut(auth[0], " ")
		if ok && strings.EqualFold(scheme, "Basic") && len(encoded) == base64.StdEncoding.EncodedLen(71) {
			n, err := base64.StdEncoding.Decode(credentials[:], []byte(encoded))
			if err == nil {
				decoded = n
			}
		}
	}
	if token == "" || decoded != 71 || string(credentials[:7]) != "tether:" || subtle.ConstantTimeCompare(credentials[7:71], []byte(token)) != 1 {
		w.Header().Set("Connection", "close")
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		w.Header().Set("Proxy-Authenticate", "Basic realm="+strconv.Quote(realm))
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if !connected || ctx == nil || ctx.Err() != nil || socket == "" || proxy == nil {
		http.Error(w, "SSH route unavailable", http.StatusServiceUnavailable)
		return
	}
	requestCtx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	r = r.WithContext(requestCtx)
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Authenticate")
	if r.Method == http.MethodConnect {
		serveSSHConnect(w, r, socket)
		return
	}
	if (r.URL.Scheme != "http" && r.URL.Scheme != "https") || r.URL.Host == "" {
		http.Error(w, "absolute HTTP URL required", http.StatusBadRequest)
		return
	}
	proxy.ServeHTTP(w, r)
}

type sshProxyRequestContextKey struct{}

func newSSHHTTPProxy(sessionCtx context.Context, socketPath string) (*httputil.ReverseProxy, *http.Transport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Never fall back to the workstation or an environment proxy.
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(sessionCtx, cancel)
		defer stop()
		defer cancel()
		// Transport detaches dialing from request cancellation for pool reuse.
		// Keep the original request cancellable through the SOCKS negotiation.
		if requestCtx, ok := ctx.Value(sshProxyRequestContextKey{}).(context.Context); ok {
			stopRequest := context.AfterFunc(requestCtx, cancel)
			defer stopRequest()
		}
		conn, err := dialSSHProxy(dialCtx, socketPath, address)
		if err != nil {
			return nil, err
		}
		return &sshProxyConn{Conn: conn, stop: context.AfterFunc(sessionCtx, func() { _ = conn.Close() })}, nil
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out = p.Out.WithContext(context.WithValue(p.Out.Context(), sshProxyRequestContextKey{}, p.In.Context()))
			p.Out.Host = p.In.Host
			p.Out.URL.RawQuery = p.In.URL.RawQuery
			// This is a forward proxy. Preserve end-to-end caller metadata,
			// but never restore a header nominated as hop-by-hop.
			for _, name := range [...]string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if values := p.In.Header.Values(name); values != nil {
					p.Out.Header[name] = values
				}
			}
			for _, connection := range p.In.Header.Values("Connection") {
				for connection != "" {
					token, rest, _ := strings.Cut(connection, ",")
					connection = rest
					token = strings.TrimSpace(token)
					if strings.EqualFold(token, "Forwarded") || strings.EqualFold(token, "X-Forwarded-For") ||
						strings.EqualFold(token, "X-Forwarded-Host") || strings.EqualFold(token, "X-Forwarded-Proto") {
						p.Out.Header.Del(token)
					}
				}
			}
			p.Out.Header.Del("Proxy-Authorization")
			p.Out.Header.Del("Proxy-Authenticate")
			p.Out.Header.Del("Proxy-Connection")
			p.Out.Trailer.Del("Proxy-Authorization")
			p.Out.Trailer.Del("Proxy-Authenticate")
		},
		ModifyResponse: func(r *http.Response) error {
			r.Header.Del("Proxy-Authenticate")
			r.Header.Del("Proxy-Authorization")
			r.Trailer.Del("Proxy-Authenticate")
			r.Trailer.Del("Proxy-Authorization")
			if r.StatusCode == http.StatusProxyAuthRequired {
				return errors.New("origin returned a proxy authentication challenge")
			}
			if r.StatusCode != http.StatusSwitchingProtocols {
				r.Body = &sshProxyBody{ReadCloser: r.Body, trailer: r.Trailer}
			}
			return nil
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "SSH proxy request failed", http.StatusBadGateway)
		},
	}
	return proxy, transport
}

// Trailers arrive only when the response body ends, after ModifyResponse.
type sshProxyBody struct {
	io.ReadCloser
	trailer http.Header
}

func (b *sshProxyBody) Close() error {
	err := b.ReadCloser.Close()
	b.trailer.Del("Proxy-Authenticate")
	b.trailer.Del("Proxy-Authorization")
	return err
}

// Remove the cancellation registration when the transport releases a connection.
// A pool belongs to one SSH attempt, so cancellation also closes idle connections.
type sshProxyConn struct {
	net.Conn
	stop func() bool
}

func (c *sshProxyConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

func serveSSHConnect(w http.ResponseWriter, r *http.Request, socket string) {
	if _, err := sshSOCKSAddress(r.Host); err != nil {
		http.Error(w, "invalid CONNECT destination", http.StatusBadRequest)
		return
	}
	upstream, err := dialSSHProxy(r.Context(), socket, r.Host)
	if err != nil {
		http.Error(w, "SSH tunnel unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "HTTP tunneling unsupported", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	stop := context.AfterFunc(r.Context(), func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stop()
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	copied := make(chan struct{})
	go func() {
		// The HTTP parser may already have buffered the first tunnel bytes.
		_, _ = io.Copy(upstream, buffered.Reader)
		_ = upstream.Close()
		_ = client.Close()
		close(copied)
	}()
	_, _ = io.Copy(client, upstream)
	_ = upstream.Close()
	_ = client.Close()
	<-copied
}

// Encode names verbatim for remote resolution; never invoke the local resolver.
func sshSOCKSAddress(address string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(address)
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || port < 1 || port > 65535 || host == "" ||
		strings.ContainsAny(host, " \t\r\n/%@\\") || strings.ContainsAny(portText, "+-") {
		return nil, errors.New("invalid proxy destination")
	}
	request := make([]byte, 3, 3+1+1+255+2)
	request[0], request[1] = 5, 1
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			request = append(request, 1)
			request = append(request, v4...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 || strings.Contains(host, ":") || strings.ContainsFunc(host, func(r rune) bool { return r < 33 || r == 127 }) {
			return nil, errors.New("invalid proxy hostname")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	return binary.BigEndian.AppendUint16(request, uint16(port)), nil
}

func dialSSHProxy(ctx context.Context, socketPath, address string) (net.Conn, error) {
	request, err := sshSOCKSAddress(address)
	if err != nil {
		return nil, err
	}
	if err := protocol.ValidatePrivateSocket(socketPath); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	if err := protocol.VerifyPeerCredentials(conn); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return nil, err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return nil, err
	}
	if greeting != [2]byte{5, 0} {
		return nil, errors.New("private SOCKS authentication negotiation failed")
	}
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return nil, errors.New("SSH SOCKS forwarding rejected")
	}
	size := 0
	switch reply[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return nil, err
		}
		size = int(length[0])
	default:
		return nil, errors.New("invalid SOCKS reply address type")
	}
	var bound [257]byte
	if _, err := io.ReadFull(conn, bound[:size+2]); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	ok = true
	return conn, nil
}
