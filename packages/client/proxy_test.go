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

	proxy := NewProxy("127.0.0.1:0")
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()

	// Enroll localhost:3000 to the echo server
	proxy.Enroll("localhost:3000", echoAddr)

	proxyConn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer proxyConn.Close()

	// Send HTTP CONNECT for enrolled target
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

	// Consume empty line
	_, _ = br.ReadString('\n')

	// Send test payload through the spliced tunnel
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

func TestProxyConnectExternalTarget(t *testing.T) {
	echoLn, echoAddr := startMockTCPEchoServer(t)
	defer echoLn.Close()

	proxy := NewProxy("127.0.0.1:0")
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()

	// Do not enroll: connect directly to echoAddr as an external host
	proxyConn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer proxyConn.Close()

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echoAddr, echoAddr)
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

	testPayload := "direct external traffic\n"
	if _, err := proxyConn.Write([]byte(testPayload)); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	echoed, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if echoed != testPayload {
		t.Fatalf("expected %q, got %q", testPayload, echoed)
	}
}

func TestProxyPlainHTTPReverseProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "dev.local:3000" {
			t.Errorf("expected preserved host dev.local:3000, got: %s", r.Host)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Hello Tether Plain HTTP"))
	}))
	defer backend.Close()

	backendAddr := strings.TrimPrefix(backend.URL, "http://")

	proxy := NewProxy("127.0.0.1:0")
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Close()

	proxy.Enroll("dev.local:3000", backendAddr)

	req, err := http.NewRequest(http.MethodGet, "http://dev.local:3000/test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got: %d", res.StatusCode)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != "Hello Tether Plain HTTP" {
		t.Fatalf("expected body 'Hello Tether Plain HTTP', got %q", string(body))
	}
}

func TestProxyUnenroll(t *testing.T) {
	proxy := NewProxy("127.0.0.1:0")
	proxy.Enroll("target.local", "127.0.0.1:9999")

	if _, ok := proxy.ResolveTarget("target.local"); !ok {
		t.Fatalf("expected target.local to be enrolled")
	}

	proxy.Unenroll("target.local")
	if _, ok := proxy.ResolveTarget("target.local"); ok {
		t.Fatalf("expected target.local to be unenrolled")
	}
}
