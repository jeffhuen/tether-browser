package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestPlaintextSquatterReceivesNeitherKeyNorCommand(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	observed := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			observed <- nil
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		data := make([]byte, 4096)
		n, _ := conn.Read(data)
		observed <- data[:n]
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":"req-1","result":{"connected":true},"seq":1,"epoch":"forged"}` + "\n"))
	}()
	caller := NewClient(listener.Addr().String())
	caller.SetToken("SECRET-KEY-FIXTURE")
	caller.SetTimeout(time.Second)
	_, err = caller.Call(context.Background(), protocol.MethodFill, protocol.FillParams{Selector: "#password", Text: "sensitive-input-fixture"})
	if err == nil {
		t.Fatal("plaintext squatter accepted")
	}
	wire := <-observed
	if bytes.Contains(wire, []byte("SECRET-KEY-FIXTURE")) || bytes.Contains(wire, []byte("sensitive-input-fixture")) || bytes.Contains(wire, []byte(protocol.MethodFill)) {
		t.Fatal("credential or command reached unverified daemon")
	}
}

func TestUnixCallerRejectsPermissiveBrokerBeforeSending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	caller := NewClient("unix:" + path)
	if _, err := caller.Call(context.Background(), protocol.MethodFill, protocol.FillParams{Selector: "#password", Text: "sensitive"}); err == nil {
		t.Fatal("permissive broker accepted")
	}
}
