package client

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestServerRejectsOversizedInputsAndUnauthenticatedPeers(t *testing.T) {
	server := NewServer(&mockDriver{})
	server.SetAuthToken("boundary-key")
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe(0) }()
	defer func() {
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for server.Port() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Port() == 0 {
		t.Fatal("server did not bind")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", server.Port())
	for _, tc := range []struct {
		name, input string
		status      int
	}{
		{"HTTP advertised oversized body", "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 1073741824\r\nConnection: close\r\n\r\n", 413},
		{"HTTP oversized header", "POST / HTTP/1.1\r\nHost: localhost\r\nX-Large: " + strings.Repeat("a", 32<<10) + "\r\n\r\n", 431},
		{"newline oversized request", `{"method":"` + strings.Repeat("a", protocol.MaxCommandBytes) + "\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := protocol.DaemonTLS("boundary-key", false)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := tls.Dial("tcp", addr, config)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = conn.Write([]byte(tc.input))
			if tc.status == 0 {
				var p [1]byte
				if _, err := conn.Read(p[:]); err == nil {
					t.Fatal("oversized frame accepted")
				}
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(line, fmt.Sprintf(" %d ", tc.status)) {
				t.Fatalf("got %q", line)
			}
		})
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"browser.status\"}\n"))
	var p [128]byte
	n, _ := conn.Read(p[:])
	if strings.Contains(string(p[:n]), "connected") {
		t.Fatal("plaintext peer reached dispatch")
	}
	config, _ := protocol.DaemonTLS("boundary-key", false)
	config.Certificates = nil
	secured, err := tls.Dial("tcp", addr, config)
	if err == nil {
		defer secured.Close()
		_ = secured.SetDeadline(time.Now().Add(time.Second))
		_, _ = secured.Write([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"browser.status\"}\n"))
		if n, err := secured.Read(p[:]); err == nil || strings.Contains(string(p[:n]), "connected") {
			t.Fatal("peer without client identity reached dispatch")
		}
	}
}

func TestKeylessServerFailsBeforeBinding(t *testing.T) {
	server := NewServer(&mockDriver{})
	if err := server.ListenAndServe(0); err == nil || server.Port() != 0 {
		t.Fatal("keyless daemon accepted a listener")
	}
}
