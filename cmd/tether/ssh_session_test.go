package main

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSSH replaces runSSH. Attempts in up verify forwarding, attempts in fail
// return that error, and any other attempt blocks until cancelled.
func fakeSSH(t *testing.T, delay time.Duration, up map[int32]bool, fail map[int32]string) (*atomic.Int32, chan int32) {
	t.Helper()
	run, oldDelay := runSSH, reconnectDelay
	t.Cleanup(func() { runSSH, reconnectDelay = run, oldDelay })
	reconnectDelay = delay
	attempts, started := &atomic.Int32{}, make(chan int32, 16)
	runSSH = func(s *sshSession, ctx context.Context, _ sshConnectionStatus) error {
		n := attempts.Add(1)
		started <- n
		if up[n] {
			s.mu.Lock()
			s.upstream = 1
			s.mu.Unlock()
		}
		if msg, ok := fail[n]; ok {
			return errors.New(msg)
		}
		<-ctx.Done()
		return nil
	}
	return attempts, started
}

func waitStatus(t *testing.T, s *sshSession, done func(sshConnectionStatus) bool) sshConnectionStatus {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if status := s.Status(); done(status) {
			return status
		}
	}
	t.Fatalf("status never reached; last %+v", s.Status())
	return sshConnectionStatus{}
}

func TestDroppedSessionReconnectsUntilDisconnect(t *testing.T) {
	attempts, started := fakeSSH(t, time.Millisecond,
		map[int32]bool{1: true}, map[int32]string{1: "SSH connection closed: relay stalled", 2: "ssh: connect timed out"})
	session := &sshSession{}
	defer session.Close()
	if _, err := session.Connect(context.Background(), "host", 0); err != nil {
		t.Fatal(err)
	}
	// Attempt 2 fails while the link is still down; attempt 3 must still happen.
	for want := int32(1); want <= 3; want++ {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("attempt %d started, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("attempt %d never started after the session dropped", want)
		}
	}
	if status := session.Status(); status.State != "connecting" || status.Error != "ssh: connect timed out" {
		t.Fatalf("reconnecting status = %+v", status)
	}
	if status := session.Disconnect(); status.State != "disconnected" {
		t.Fatalf("Disconnect status = %+v", status)
	}
	session.Close()
	if n := attempts.Load(); n != 3 {
		t.Fatalf("%d attempts after Disconnect, want 3", n)
	}
}

func TestSessionThatNeverConnectsDoesNotRetry(t *testing.T) {
	attempts, _ := fakeSSH(t, time.Millisecond, nil, map[int32]string{1: "SSH could not connect: Permission denied"})
	session := &sshSession{}
	defer session.Close()
	if _, err := session.Connect(context.Background(), "host", 0); err != nil {
		t.Fatal(err)
	}
	status := waitStatus(t, session, func(s sshConnectionStatus) bool { return s.State == "disconnected" })
	time.Sleep(20 * time.Millisecond)
	if n := attempts.Load(); n != 1 || status.Error != "SSH could not connect: Permission denied" {
		t.Fatalf("attempts=%d status=%+v; a failed first connection must stop and report", n, status)
	}
}

func TestDisconnectInterruptsReconnectBackoff(t *testing.T) {
	attempts, _ := fakeSSH(t, time.Hour, map[int32]bool{1: true}, map[int32]string{1: "SSH connection closed"})
	session := &sshSession{}
	if _, err := session.Connect(context.Background(), "host", 0); err != nil {
		t.Fatal(err)
	}
	// Connect starts in "connecting"; the drop's error proves the backoff wait began.
	waitStatus(t, session, func(s sshConnectionStatus) bool { return s.State == "connecting" && s.Error != "" })
	session.Disconnect()
	closed := make(chan struct{})
	go func() {
		session.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not end the reconnect wait")
	}
	if n := attempts.Load(); n != 1 {
		t.Fatalf("%d attempts, want 1", n)
	}
}

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
