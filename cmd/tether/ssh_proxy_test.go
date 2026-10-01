package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A real private Unix SOCKS listener stands in for OpenSSH, recording the wire
// destination before routing to real local origins. No local DNS is involved.
func sshProxyFixture(t *testing.T, route func(string) string) (string, *atomic.Int32, <-chan string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "socks")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	addresses := make(chan string, 64)
	var mu sync.Mutex
	active := make(map[net.Conn]bool)
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			mu.Lock()
			active[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(active, conn); mu.Unlock() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil {
					return
				}
				if greeting != [3]byte{5, 1, 0} {
					t.Errorf("unsafe SOCKS negotiation: %v", greeting)
					return
				}
				_, _ = conn.Write([]byte{5, 0})
				var head [4]byte
				if _, err := io.ReadFull(conn, head[:]); err != nil {
					return
				}
				if head[0] != 5 || head[1] != 1 || head[2] != 0 {
					t.Errorf("invalid SOCKS request: %v", head)
					return
				}
				size := 0
				switch head[3] {
				case 1:
					size = 4
				case 4:
					size = 16
				case 3:
					var length [1]byte
					if _, err := io.ReadFull(conn, length[:]); err != nil {
						return
					}
					size = int(length[0])
				default:
					t.Errorf("invalid address type: %v", head)
					return
				}
				address := make([]byte, size+2)
				if _, err := io.ReadFull(conn, address); err != nil {
					return
				}
				host := string(address[:size])
				if head[3] != 3 {
					host = net.IP(address[:size]).String()
				}
				dest := net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(address[size:])))
				addresses <- dest
				target := route(dest)
				if target == "" {
					// Deliberately hold the negotiation open to exercise cancellation.
					_, _ = io.Copy(io.Discard, conn)
					return
				}
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				// Exercise every variable-length reply, not a fixed ten-byte read.
				switch head[3] {
				case 3:
					_, _ = conn.Write([]byte{5, 0, 0, 3, 3, 's', 's', 'h', 0, 1})
				case 4:
					_, _ = conn.Write(append([]byte{5, 0, 0, 4}, make([]byte, 18)...))
				default:
					_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
				}
				_ = conn.SetDeadline(time.Time{})
				copied := make(chan struct{})
				go func() {
					_, _ = io.Copy(upstream, conn)
					_ = upstream.Close()
					_ = conn.Close()
					close(copied)
				}()
				_, _ = io.Copy(conn, upstream)
				_ = conn.Close()
				_ = upstream.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-acceptDone
		mu.Lock()
		for conn := range active {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return path, &count, addresses
}

func startSSHProxyFixture(t *testing.T, socket string) (*sshSession, string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	proxy, transport := newSSHHTTPProxy(ctx, socket)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshSession{
		ctx: ctx, cancel: cancel, upstream: socket, httpProxy: proxy,
		proxyToken: strings.Repeat("a", 64), proxyRealm: "tether-test",
		status: sshConnectionStatus{State: "connected"}, listener: ln,
	}
	done := make(chan struct{})
	go func() { s.serveProxy(ln); close(done) }()
	t.Cleanup(func() { s.Close(); transport.CloseIdleConnections(); <-done })
	return s, ln.Addr().String(), cancel
}

func sshProxyAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("tether:"+strings.Repeat("a", 64)))
}

func TestSSHAuthenticatedHTTPProxy(t *testing.T) {
	type received struct {
		uri, body, auth, cookie, proxyAuth, proxyChallenge string
		headers                                            http.Header
	}
	requests := make(chan received, 16)
	streamClosed := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- received{r.RequestURI, string(body), r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("Proxy-Authorization"), r.Header.Get("Proxy-Authenticate"), r.Header.Clone()}
		w.Header().Set("Proxy-Authenticate", `Basic realm="hostile-origin"`)
		w.Header().Set("Proxy-Authorization", "must-not-leak")
		switch r.URL.Path {
		case "/stream":
			_, _ = io.WriteString(w, "streaming")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(streamClosed)
			return
		case "/407":
			w.WriteHeader(407)
		case "/401":
			w.Header().Set("WWW-Authenticate", `Basic realm="origin"`)
			w.WriteHeader(401)
		case "/trailers":
			w.Header().Set("Trailer", "Proxy-Authenticate, Proxy-Authorization, X-End")
			_, _ = io.WriteString(w, "stream")
			w.Header().Set("Proxy-Authenticate", "hostile-trailer")
			w.Header().Set("Proxy-Authorization", "hostile-secret")
			w.Header().Set("X-End", "complete")
			return
		}
		_, _ = io.WriteString(w, "origin:"+string(body))
	}))
	defer origin.Close()
	path, dials, addresses := sshProxyFixture(t, func(string) string { return origin.Listener.Addr().String() })
	_, address, cancel := startSSHProxyFixture(t, path)
	proxyURL, _ := url.Parse("http://" + address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, auth := range []string{"", "Basic ", "Basic Og==", "Bearer token", "Basic " + base64.StdEncoding.EncodeToString([]byte("other:"+strings.Repeat("a", 64))), sshProxyAuth() + "!"} {
		req, _ := http.NewRequest("POST", "http://must-not-resolve.invalid/upload", strings.NewReader("secret-body"))
		req.Header.Set("Proxy-Authorization", auth)
		// Origin credentials must never authorize the local proxy.
		req.Header.Set("Authorization", sshProxyAuth())
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 407 || resp.Header.Get("Proxy-Authenticate") != `Basic realm="tether-test"` {
			t.Fatalf("auth %q: status=%d challenge=%q", auth, resp.StatusCode, resp.Header.Get("Proxy-Authenticate"))
		}
	}
	if dials.Load() != 0 {
		t.Fatal("unauthenticated request reached SSH")
	}
	// Rejection must close the socket without waiting for an untrusted body.
	rejected, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Close()
	_ = rejected.SetDeadline(time.Now().Add(time.Second))
	_, _ = fmt.Fprint(rejected, "POST http://must-not-resolve.invalid/ HTTP/1.1\r\nHost: must-not-resolve.invalid\r\nContent-Length: 1024\r\n\r\n")
	rejectedReader := bufio.NewReader(rejected)
	rejection, err := http.ReadResponse(rejectedReader, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rejection.Body)
	_ = rejection.Body.Close()
	if rejection.StatusCode != 407 {
		t.Fatalf("unauthenticated body status = %d", rejection.StatusCode)
	}
	if _, err := rejectedReader.ReadByte(); err != io.EOF {
		t.Fatalf("rejected connection was not closed: %v", err)
	}
	_ = rejected.Close()
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/a%2Fb?q=x%2Fy", 200, "origin:payload"}, {"/search?filter=a;b&keep=1", 200, "origin:payload"},
		{"/hop", 200, "origin:payload"}, {"/401", 401, "origin:payload"},
		{"/407", 502, "SSH proxy request failed\n"}, {"/trailers", 200, "stream"},
	} {
		req, _ := http.NewRequest("POST", "http://remote-dns.invalid"+tc.path, strings.NewReader("payload"))
		req.Header.Set("Proxy-Authorization", sshProxyAuth())
		req.Header.Set("Proxy-Authenticate", "local-challenge")
		req.Header.Set("Authorization", "Bearer origin-token")
		req.Header.Set("Cookie", "session=origin-cookie")
		forwarding := http.Header{
			"Forwarded":         {"for=192.0.2.1;proto=https", "for=192.0.2.2"},
			"X-Forwarded-For":   {"192.0.2.1"},
			"X-Forwarded-Host":  {"origin.example"},
			"X-Forwarded-Proto": {"https"},
		}
		for name, values := range forwarding {
			req.Header[name] = values
		}
		if tc.path == "/hop" {
			req.Header.Add("Connection", "keep-alive, x-forwarded-proto")
			req.Header.Add("Connection", " FORWARDED ")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status || string(body) != tc.body {
			t.Fatalf("%s: status=%d body=%q error=%v", tc.path, resp.StatusCode, body, err)
		}
		if resp.Header.Get("Proxy-Authenticate") != "" || resp.Header.Get("Proxy-Authorization") != "" || resp.Trailer.Get("Proxy-Authenticate") != "" || resp.Trailer.Get("Proxy-Authorization") != "" {
			t.Fatal("origin proxy credentials/challenge leaked")
		}
		if tc.status == 401 && resp.Header.Get("WWW-Authenticate") != `Basic realm="origin"` {
			t.Fatal("origin authentication was stripped")
		}
		if tc.path == "/trailers" && resp.Trailer.Get("X-End") != "complete" {
			t.Fatal("ordinary response trailer lost")
		}
		got := <-requests
		if got.uri != tc.path || got.body != "payload" || got.auth != "Bearer origin-token" || got.cookie != "session=origin-cookie" || got.proxyAuth != "" || got.proxyChallenge != "" {
			t.Errorf("origin request changed/leaked: %+v", got)
		}
		for name, values := range forwarding {
			if tc.path == "/hop" && (name == "Forwarded" || name == "X-Forwarded-Proto") {
				values = nil
			}
			if !slices.Equal(got.headers.Values(name), values) {
				t.Errorf("%s: origin %s = %q, want %q", tc.path, name, got.headers.Values(name), values)
			}
		}
	}
	select {
	case got := <-addresses:
		if got != "remote-dns.invalid:80" {
			t.Fatalf("DNS was not remote: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no SOCKS destination")
	}
	streamReq, _ := http.NewRequest("GET", "http://remote-dns.invalid/stream", nil)
	streamReq.Header.Set("Proxy-Authorization", sshProxyAuth())
	streamResp, err := client.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len("streaming"))
	if _, err := io.ReadFull(streamResp.Body, first); err != nil || string(first) != "streaming" {
		t.Fatalf("response did not stream: %q %v", first, err)
	}
	cancel()
	streamReadDone := make(chan error, 1)
	go func() { _, err := io.ReadAll(streamResp.Body); streamReadDone <- err }()
	select {
	case err := <-streamReadDone:
		if err == nil {
			t.Fatal("cancelled response stream remained usable")
		}
	case <-time.After(time.Second):
		_ = streamResp.Body.Close()
		t.Fatal("session cancellation did not interrupt the response stream")
	}
	_ = streamResp.Body.Close()
	select {
	case <-streamClosed:
	case <-time.After(time.Second):
		t.Fatal("session cancellation did not close the origin request")
	}
	req, _ := http.NewRequest("GET", "http://remote-dns.invalid/offline", nil)
	req.Header.Set("Proxy-Authorization", sshProxyAuth())
	before := dials.Load()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 503 || dials.Load() != before {
		t.Fatal("unavailable route did not fail closed")
	}
}

func TestSSHProxyCONNECTAndWebSocket(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer origin-token" {
			t.Error("upgrade leaked proxy auth or lost origin auth")
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nProxy-Authenticate: hostile-origin\r\n\r\n")
		_ = buffered.Flush()
		_, _ = io.Copy(conn, buffered.Reader)
	}))
	defer origin.Close()
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	path, _, _ := sshProxyFixture(t, func(dest string) string {
		if strings.HasSuffix(dest, ":443") {
			return echo.Addr().String()
		}
		return origin.Listener.Addr().String()
	})
	_, address, cancel := startSSHProxyFixture(t, path)
	var clients []net.Conn
	defer func() {
		for _, conn := range clients {
			_ = conn.Close()
		}
		_ = echo.Close()
		<-echoDone
	}()
	for _, tc := range []struct {
		name, request, payload string
		status                 int
	}{
		{"CONNECT", "CONNECT remote-dns.invalid:443 HTTP/1.1\r\nHost: remote-dns.invalid:443\r\n", "early-tunnel-payload", 200},
		{"WebSocket", "GET http://remote-dns.invalid/ws HTTP/1.1\r\nHost: remote-dns.invalid\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nAuthorization: Bearer origin-token\r\n", "\x81\x85mask\x05\x04\x1f\x07\x02", 101},
	} {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, conn)
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		early := ""
		if tc.status == 200 {
			early = tc.payload
		}
		_, err = io.WriteString(conn, tc.request+"Proxy-Authorization: "+sshProxyAuth()+"\r\n\r\n"+early)
		if err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.status || resp.Header.Get("Proxy-Authenticate") != "" {
			t.Fatalf("%s: status=%d headers=%v", tc.name, resp.StatusCode, resp.Header)
		}
		if tc.status == 101 {
			if _, err := io.WriteString(conn, tc.payload); err != nil {
				t.Fatal(err)
			}
		}
		got := make([]byte, len(tc.payload))
		if _, err := io.ReadFull(reader, got); err != nil || string(got) != tc.payload {
			t.Fatalf("%s payload=%q err=%v", tc.name, got, err)
		}
	}
	cancel()
	for _, conn := range clients {
		var b [1]byte
		if _, err := conn.Read(b[:]); err == nil {
			t.Fatal("session cancellation left a tunnel open")
		} else if n, ok := err.(net.Error); ok && n.Timeout() {
			t.Fatal("tunnel did not close on cancellation")
		}
	}
}

func TestSSHProxyPrivateDialBoundaries(t *testing.T) {
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	path, dials, addresses := sshProxyFixture(t, func(string) string { return echo.Addr().String() })
	for _, address := range []string{"remote.invalid:80", "127.0.0.1:443", "[2001:db8::1]:8443"} {
		conn, err := dialSSHProxy(context.Background(), path, address)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(conn, "after-reply")
		got := make([]byte, len("after-reply"))
		_, err = io.ReadFull(conn, got)
		_ = conn.Close()
		if err != nil || string(got) != "after-reply" {
			t.Fatalf("variable SOCKS reply consumed payload: %q %v", got, err)
		}
		if got := <-addresses; got != address {
			t.Fatalf("wire destination: %s, want %s", got, address)
		}
	}
	before := dials.Load()
	for _, address := range []string{"host", ":80", "host:0", "host:65536", "host:+80", "bad/name:80", "bad host:80", "[bad:ipv6]:80"} {
		if conn, err := dialSSHProxy(context.Background(), path, address); err == nil {
			_ = conn.Close()
			t.Fatalf("invalid destination accepted: %q", address)
		}
	}
	if dials.Load() != before {
		t.Fatal("invalid address dispatched to SSH")
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if conn, err := dialSSHProxy(context.Background(), path, "host:80"); err == nil {
		_ = conn.Close()
		t.Fatal("public Unix socket accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(path), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if conn, err := dialSSHProxy(context.Background(), alias, "host:80"); err == nil {
		_ = conn.Close()
		t.Fatal("symlink socket accepted")
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if conn, err := dialSSHProxy(context.Background(), path, "host:80"); err == nil {
		_ = conn.Close()
		t.Fatal("public parent directory accepted")
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	stalled, _, observed := sshProxyFixture(t, func(string) string { return "" })
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		conn, err := dialSSHProxy(ctx, stalled, "remote.invalid:80")
		if conn != nil {
			_ = conn.Close()
		}
		finished <- err
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("negotiation did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("handshake ignored cancellation")
	}
	for _, tc := range []struct {
		name     string
		greeting [2]byte
		reply    []byte
	}{
		{"non-noauth", [2]byte{5, 2}, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}},
		{"wrong-greeting-version", [2]byte{4, 0}, []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}},
		{"denied", [2]byte{5, 0}, []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 1}},
		{"wrong-reply-version", [2]byte{5, 0}, []byte{4, 0, 0, 1, 127, 0, 0, 1, 0, 1}},
		{"reserved-byte", [2]byte{5, 0}, []byte{5, 0, 1, 1, 127, 0, 0, 1, 0, 1}},
		{"unknown-address-type", [2]byte{5, 0}, []byte{5, 0, 0, 9, 127, 0, 0, 1, 0, 1}},
	} {
		badPath := filepath.Join(t.TempDir(), "socks")
		ln, err := net.Listen("unix", badPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(badPath, 0600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			var greeting [3]byte
			if _, err := io.ReadFull(conn, greeting[:]); err != nil {
				return
			}
			_, _ = conn.Write(tc.greeting[:])
			var request [11]byte // SOCKS request for test:80.
			if _, err := io.ReadFull(conn, request[:]); err != nil {
				return
			}
			_, _ = conn.Write(tc.reply)
		}()
		conn, err := dialSSHProxy(context.Background(), badPath, "test:80")
		if conn != nil {
			_ = conn.Close()
		}
		_ = ln.Close()
		<-done
		if err == nil {
			t.Fatalf("unsafe SOCKS handshake accepted: %s", tc.name)
		}
	}
}

func TestPrivateSSHAddressParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want string
	}{
		{"remote DNS", []byte{5, 1, 0, 3, 3, 'a', '.', 'b', 0, 80}, "a.b:80"},
		{"IPv4", []byte{5, 1, 0, 1, 127, 0, 0, 1, 1, 187}, "127.0.0.1:443"},
		{"IPv6", []byte{5, 1, 0, 4, 0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0x20, 0xfb}, "[2001:db8::1]:8443"},
		{"zero port", []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 0}, ""},
		{"empty name", []byte{5, 1, 0, 3, 0}, ""},
		{"unsupported command", []byte{5, 2, 0, 1}, ""},
		{"unsupported address", []byte{5, 1, 0, 9}, ""},
		{"truncated address", []byte{5, 1, 0, 1, 127}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(5 * time.Second))
			go func() {
				defer client.Close()
				_, _ = client.Write([]byte{5, 1, 0})
				var reply [2]byte
				if _, err := io.ReadFull(client, reply[:]); err == nil {
					_, _ = client.Write(tc.wire)
				}
			}()
			got, err := readPrivateSSHAddress(server)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("destination=%q error=%v; want %q", got, err, tc.want)
			}
		})
	}
}
