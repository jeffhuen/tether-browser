package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignInBannerSurvivesLargeAndSplitDiagnosticLines(t *testing.T) {
	url := "https://login.tailscale.com/a/1a2b3c4d5e6f"
	for _, parts := range [][]string{
		{checkBanner + strings.Repeat("x", 6000) + "\n"},
		{"Sign in: " + url + " " + strings.Repeat("x", 6000), "\n"},
		{checkBanner[:len(checkBanner)-6], checkBanner[len(checkBanner)-6:] + strings.Repeat("x", 6000) + "\n"},
	} {
		var got []string
		buffer := &sshErrorBuffer{onSignIn: func(value string) { got = append(got, value) }}
		for _, part := range parts {
			_, _ = buffer.Write([]byte(part))
		}
		if len(got) != 1 || got[0] != url {
			t.Fatalf("banner was lost or split: %v", got)
		}
		if len(buffer.String()) > 4096 {
			t.Fatal("diagnostic tail exceeded bound")
		}
	}
}

func TestPreReadyFailureIsNotSignInTimeoutAndNetBirdAuthStopsRetry(t *testing.T) {
	previous, delay := runSSH, reconnectDelay
	defer func() { runSSH, reconnectDelay = previous, delay }()
	reconnectDelay = time.Millisecond
	for _, tc := range []struct {
		name             string
		netbird, timeout bool
	}{
		{"authenticated remote setup failure", false, false},
		{"actual elapsed sign-in timeout", false, true},
		{"NetBird authentication helper failure after prior connection", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			remoteErr := errors.New("SSH could not connect: read-only remote cache")
			runSSH = func(s *sshSession, ctx context.Context, status sshConnectionStatus) error {
				attempt := attempts.Add(1)
				if tc.netbird && attempt == 1 {
					s.mu.Lock()
					s.upstream = "fixture"
					s.mu.Unlock()
					return errors.New("SSH network connection closed")
				}
				if tc.netbird {
					s.setAuthWait(status.SessionID)
				} else {
					s.setSignInURL(status.SessionID, "https://login.tailscale.com/a/123")
				}
				if tc.timeout {
					return errSignInTimeout
				}
				return remoteErr
			}
			session := &sshSession{}
			defer session.Close()
			if _, err := session.Connect(context.Background(), "fixture-host", 0); err != nil {
				t.Fatal(err)
			}
			status := waitStatus(t, session, func(status sshConnectionStatus) bool { return status.State == "disconnected" })
			time.Sleep(20 * time.Millisecond)
			wantAttempts := int32(1)
			if tc.netbird {
				wantAttempts = 2
			}
			if attempts.Load() != wantAttempts || status.SignInRequired != tc.timeout {
				t.Fatalf("unexpected authentication retry/classification: attempts=%d status=%+v", attempts.Load(), status)
			}
			if !tc.timeout && status.Error != remoteErr.Error() {
				t.Fatal("remote failure cause was hidden")
			}
		})
	}
}

func TestNetBirdConfiguredProxyAndExactWaitPrompt(t *testing.T) {
	for text, want := range map[string]bool{
		"proxycommand /usr/bin/netbird ssh proxy %h %p":     true,
		"proxycommand exec netbird ssh proxy %h %p":         true,
		"proxycommand /usr/bin/not-netbird ssh proxy %h %p": false,
		"proxycommand sh -c 'netbird ssh proxy %h %p'":      false,
		"proxycommand none":                                 false,
	} {
		if got := netBirdConfig(text); got != want {
			t.Fatalf("configured proxy %q: %v", text, got)
		}
	}
	session := &sshSession{status: sshConnectionStatus{State: "connecting", SessionID: "attempt"}}
	buffer := &sshErrorBuffer{onAuthWait: func() { session.setAuthWait("attempt") }}
	_, _ = buffer.Write([]byte("SSH authentication required.\nhttps://identity.example/oidc\nWaiting for authentic"))
	_, _ = buffer.Write([]byte("ation...\n"))
	status := session.Status()
	if status.AuthProvider != "NetBird" || status.AuthMessage == "" || status.SignInURL != "" {
		t.Fatalf("bad provider wait status: %+v", status)
	}
	session.completeSignIn("attempt")
	_, _ = buffer.Write([]byte("SSH authentication required.\n"))
	if session.Status().AuthProvider != "" {
		t.Fatal("late authentication prompt reopened connected session")
	}
	if !netBirdAuthFailure("Error: failed to request JWT token") || netBirdAuthFailure("Error: network connection refused") {
		t.Fatal("auth helper failure confused with network loss")
	}
}
