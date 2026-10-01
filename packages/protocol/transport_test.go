package protocol

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDaemonMutualTLSAndCertificateVerification(t *testing.T) {
	serverConfig, err := DaemonTLS("shared-key", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, serverName string
		clientAsServer          bool
		want                    bool
	}{
		{"matching identity", "shared-key", DaemonName, false, true},
		{"wrong private root", "wrong-key", DaemonName, false, false},
		{"wrong server name", "shared-key", "other.internal", false, false},
		{"client certificate cannot serve daemon", "shared-key", DaemonName, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := DaemonTLS(tc.token, false)
			if err != nil {
				t.Fatal(err)
			}
			config.ServerName = tc.serverName
			serving := serverConfig
			if tc.clientAsServer {
				serving, err = DaemonTLS("shared-key", false)
				if err != nil {
					t.Fatal(err)
				}
			}
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			_ = a.SetDeadline(time.Now().Add(time.Second))
			_ = b.SetDeadline(time.Now().Add(time.Second))
			done := make(chan error, 1)
			go func() {
				conn := tls.Server(b, serving)
				if err := conn.Handshake(); err != nil {
					done <- err
					return
				}
				_, err := conn.Write([]byte("authenticated"))
				done <- err
			}()
			conn := tls.Client(a, config)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = conn.HandshakeContext(ctx)
			if tc.want {
				if err != nil {
					t.Fatal(err)
				}
				var p [13]byte
				if _, err := conn.Read(p[:]); err != nil || string(p[:]) != "authenticated" {
					t.Fatalf("authenticated application data: %q %v", p, err)
				}
				if conn.ConnectionState().Version != tls.VersionTLS13 {
					t.Fatal("TLS1.3 was not negotiated")
				}
			} else if err == nil {
				t.Fatal("invalid identity was accepted")
			}
			a.Close()
			b.Close()
			<-done
		})
	}
	if _, err := DaemonTLS("", true); err == nil {
		t.Fatal("keyless server accepted")
	}
	if _, err := DaemonTLS("", false); err == nil {
		t.Fatal("keyless client accepted")
	}
}

func TestBoundedFramesAndResponseCorrelation(t *testing.T) {
	if _, err := ReadFrame(bufio.NewReader(strings.NewReader(strings.Repeat("x", MaxCommandBytes+1))), MaxCommandBytes); err == nil {
		t.Fatal("oversized unterminated command accepted")
	}
	req, _ := NewRequest("caller", MethodStatus, nil, 17, "session")
	for _, mutate := range []func(*Response){func(r *Response) { r.ID = json.RawMessage(`"other"`) }, func(r *Response) { r.Seq++ }, func(r *Response) { r.Epoch = "other" }} {
		resp, _ := NewResponse(req.ID, StatusResult{Connected: true}, req.Seq, req.Epoch)
		if err := ValidateResponse(req, resp); err != nil {
			t.Fatal(err)
		}
		mutate(resp)
		if ValidateResponse(req, resp) == nil {
			t.Fatal("uncorrelated response accepted")
		}
	}
}
