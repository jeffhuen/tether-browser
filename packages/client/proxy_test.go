package client

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startMockTCPEchoServer(t *testing.T) (net.Listener, string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen mock echo server: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return ln, ln.Addr().String()
}

func TestProxyConnectEnrolledTarget(t *testing.T) {
	echoLn, echoAddr := startMockTCPEchoServer(t)
	defer echoLn.Close()

	proxy := NewProxy()
	go func() {
		_ = proxy.ListenAndServe(0)
	}()
	defer proxy.Close()

	// Wait for proxy to listen
	time.Sleep(50 * time.Millisecond)

	proxy.Enroll("localhost:3000", echoAddr)

	proxyConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy.Port()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer proxyConn.Close()

	connectReq := "CONNECT localhost:3000 HTTP/1.1\r\nHost: localhost:3000\r\n\r\n"
	if _, err := proxyConn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	br := bufio.NewReader(proxyConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if !strings.Contains(statusLine, "200 Connection Established") {
		t.Fatalf("expected 200 Connection Established, got: %s", statusLine)
	}

	_, _ = br.ReadString('\n')

	testPayload := "hello tether connect tunnel\n"
	if _, err := proxyConn.Write([]byte(testPayload)); err != nil {
		t.Fatalf("write payload to tunnel: %v", err)
	}

	echoed, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read echoed payload: %v", err)
	}
	if echoed != testPayload {
		t.Fatalf("expected echo %q, got %q", testPayload, echoed)
	}
}

func TestProxyConnectUnenrolledLoopbackRejected(t *testing.T) {
	proxy := NewProxy()
	go func() {
		_ = proxy.ListenAndServe(0)
	}()
	defer proxy.Close()
	time.Sleep(50 * time.Millisecond)

	proxyConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy.Port()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer proxyConn.Close()

	// Requesting an unenrolled loopback destination MUST be rejected with 403 Forbidden
	connectReq := "CONNECT localhost:9999 HTTP/1.1\r\nHost: localhost:9999\r\n\r\n"
	if _, err := proxyConn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	br := bufio.NewReader(proxyConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(statusLine, "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for unenrolled loopback, got: %s", statusLine)
	}
}

func TestProxyPlainHTTPEnrolled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "Tether")
		fmt.Fprintf(w, "hello from remote app on %s", r.Host)
	}))
	defer ts.Close()

	proxy := NewProxy()
	go func() {
		_ = proxy.ListenAndServe(0)
	}()
	defer proxy.Close()
	time.Sleep(50 * time.Millisecond)

	tsHostPort := strings.TrimPrefix(ts.URL, "http://")
	proxy.Enroll("localhost:3000", tsHostPort)

	// Send HTTP request to proxy
	proxyConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy.Port()))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer proxyConn.Close()

	httpReq := "GET http://localhost:3000/orders HTTP/1.1\r\nHost: localhost:3000\r\n\r\n"
	if _, err := proxyConn.Write([]byte(httpReq)); err != nil {
		t.Fatalf("write http: %v", err)
	}

	br := bufio.NewReader(proxyConn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello from remote app on localhost:3000") {
		t.Fatalf("expected body with preserved Host, got: %s", string(body))
	}
}

func TestProxyRestrictedIPRejection(t *testing.T) {
	proxy := NewProxy()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	proxy.listener = ln
	proxy.port.Store(int32(ln.Addr().(*net.TCPAddr).Port))
	go func() {
		_ = http.Serve(ln, proxy)
	}()
	defer proxy.Close()

	restrictedTargets := []string{
		"127.0.0.1:3000",
		"localhost:3000",
		"test.localhost:8080",
		"[::1]:3000",
		"0.0.0.0:3000",
		"[::]:3000",
		"169.254.169.254:80",
	}

	for _, target := range restrictedTargets {
		t.Run(target, func(t *testing.T) {
			conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", proxy.Port()))
			if err != nil {
				t.Fatalf("dial proxy: %v", err)
			}
			defer conn.Close()

			connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
			if _, err := conn.Write([]byte(connectReq)); err != nil {
				t.Fatalf("write connect: %v", err)
			}

			br := bufio.NewReader(conn)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("expected 403 Forbidden for restricted target %s, got: %d", target, resp.StatusCode)
			}
		})
	}
}
