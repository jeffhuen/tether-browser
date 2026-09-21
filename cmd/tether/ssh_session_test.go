package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestSOCKSReadinessRequiresForwarding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
		ready bool
		token string
	}{
		{"forwarded", []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}, true, ""},
		{"forwarding denied", []byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0}, false, ""},
		{"greeting without forwarding", nil, false, ""},
		{"SOCKS success without daemon response", []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}, false, "fixture-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
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
				_, _ = conn.Write([]byte{5, 0})
				var request [10]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil {
					return
				}
				_, _ = conn.Write(tc.reply)
			}()
			err = probeSOCKS(context.Background(), ln.Addr().(*net.TCPAddr).Port, tc.token, "fixture-session")
			if (err == nil) != tc.ready {
				t.Fatalf("ready=%v, error=%v", tc.ready, err)
			}
			<-done
		})
	}
}

func TestCloseWaitsForSupersededConnections(t *testing.T) {
	ancestorDone := make(chan struct{})
	session := &sshSession{done: ancestorDone}
	if _, err := session.Connect(context.Background(), "first", 0); err != nil {
		close(ancestorDone)
		t.Fatal(err)
	}
	if _, err := session.Connect(context.Background(), "second", 0); err != nil {
		close(ancestorDone)
		session.Close()
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		session.Close()
		close(closed)
	}()
	select {
	case <-closed:
		close(ancestorDone)
		t.Fatal("Close returned before the superseded connection finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(ancestorDone)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after all predecessors exited")
	}
	if _, err := session.Connect(context.Background(), "third", 0); err == nil {
		t.Fatal("closed native host accepted a new SSH connection")
	}
}
