package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

type sshConnectionStatus struct {
	State     string `json:"state"`
	Host      string `json:"host"`
	ProxyPort int    `json:"proxyPort"`
	SessionID string `json:"sessionId"`
	Error     string `json:"error,omitempty"`
}

// The native host owns both the SSH child and a stable, loopback-only proxy
// listener. Losing SSH rejects new requests rather than releasing its port.
type sshSession struct {
	mu       sync.Mutex
	status   sshConnectionStatus
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	ctx      context.Context
	upstream int
	closed   bool
}

func (s *sshSession) Status() sshConnectionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	if status.State == "" {
		status.State = "disconnected"
	}
	return status
}

func (s *sshSession) Connect(parent context.Context, host string, port int) (sshConnectionStatus, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsFunc(host, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return sshConnectionStatus{}, errors.New("enter an SSH host or alias, without command-line options")
	}
	if port != 0 && (port < 1024 || port > 65535) {
		return sshConnectionStatus{}, errors.New("invalid browser proxy port")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return sshConnectionStatus{}, errors.New("native host is shutting down")
	}
	if s.status.Host == host && (s.status.State == "connected" || s.status.State == "connecting") {
		if port != 0 && port != s.status.ProxyPort {
			return sshConnectionStatus{}, errors.New("turn remote browsing off before changing proxy ports")
		}
		return s.status, nil
	}
	if s.listener == nil {
		ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return sshConnectionStatus{}, fmt.Errorf("reserve browser proxy: %w; turn remote browsing off, reconnect, then enable it again", err)
		}
		s.listener = ln
		go s.serveProxy(ln)
	} else if port != 0 && port != s.listener.Addr().(*net.TCPAddr).Port {
		return sshConnectionStatus{}, errors.New("proxy port changed; turn remote browsing off before reconnecting")
	}
	previousDone := s.done
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		cancel()
		return sshConnectionStatus{}, err
	}
	s.ctx, s.cancel, s.upstream = ctx, cancel, 0
	s.done = make(chan struct{})
	s.status = sshConnectionStatus{
		State: "connecting", Host: host,
		ProxyPort: s.listener.Addr().(*net.TCPAddr).Port,
		SessionID: hex.EncodeToString(nonce[:]),
	}
	status := s.status
	go func(done chan struct{}) {
		defer close(done)
		// Reap the previous SSH child before competing for the remote reverse port.
		if previousDone != nil {
			<-previousDone
		}
		if ctx.Err() != nil {
			return
		}
		err := s.run(ctx, status)
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.status.SessionID == status.SessionID {
			s.status.State, s.upstream = "disconnected", 0
			if err != nil {
				s.status.Error = err.Error()
			}
			if getActiveHost() == host {
				clearActiveHost()
			}
		}
	}(s.done)
	return status, nil
}

func (s *sshSession) Disconnect() sshConnectionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if getActiveHost() == s.status.Host {
		clearActiveHost()
	}
	s.status.State, s.status.SessionID, s.status.Error, s.upstream = "disconnected", "", "", 0
	return s.status
}

func (s *sshSession) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.status.State, s.upstream = "disconnected", 0
	ln, done := s.listener, s.done
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	if done != nil {
		<-done
	}
}

func (s *sshSession) serveProxy(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		ctx, port, connected := s.ctx, s.upstream, s.status.State == "connected"
		s.mu.Unlock()
		if !connected {
			_ = conn.Close()
			continue
		}
		go func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			upstream, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return
			}
			defer upstream.Close()
			stopUpstream := context.AfterFunc(ctx, func() { _ = upstream.Close() })
			defer stopUpstream()
			copied := make(chan struct{})
			go func() {
				_, _ = io.Copy(upstream, conn)
				_ = upstream.Close()
				close(copied)
			}()
			_, _ = io.Copy(conn, upstream)
			_ = conn.Close()
			<-copied
		}()
	}
}

func (s *sshSession) run(ctx context.Context, status sshConnectionStatus) error {
	token, err := cli.EnsureDaemonToken()
	if err != nil {
		return err
	}
	if strings.ContainsAny(token, "\r\n") {
		return errors.New("SSH authentication token must not contain line breaks")
	}
	if err := ensureDaemonRunning(ctx, token); err != nil {
		return err
	}
	port, err := reserveSSHPort()
	if err != nil {
		return err
	}
	controlDir, err := os.MkdirTemp("", "tether-ssh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(controlDir)
	controlPath := filepath.Join(controlDir, "s")
	// A held stdin keeps the remote cat alive. Its random readiness marker is
	// emitted only after SSH authentication and token sync.
	remote := fmt.Sprintf("umask 077 && mkdir -p ~/.cache/tether && chmod 700 ~/.cache/tether && IFS= read -r token && printf %%s \"$token\" > ~/.cache/tether/auth && unset token && chmod 600 ~/.cache/tether/auth && printf '%%s\\n' %s && cat", cli.ShellQuote(status.SessionID))
	remote = "sh -c " + cli.ShellQuote(remote)
	args := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2",
		"-o", "ExitOnForwardFailure=yes", "-o", "ForkAfterAuthentication=no",
		"-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-S", controlPath, "-o", "SessionType=default",
		"-D", fmt.Sprintf("127.0.0.1:%d", port),
		"--", status.Host, remote,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = time.Second
	stderr := &sshErrorBuffer{}
	cmd.Stderr = stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start SSH: %w", err)
	}
	// Keep the bearer token out of process arguments and process listings.
	go func() { _, _ = fmt.Fprintln(input, token) }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := make(chan bool, 1)
	connectionClosed := make(chan struct{})
	go func() {
		defer close(connectionClosed)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if scanner.Text() == status.SessionID {
				ready <- true
				_, _ = io.Copy(io.Discard, output)
				return
			}
		}
		ready <- false
	}()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case ok := <-ready:
		if !ok {
			return fmt.Errorf("SSH could not connect: %s. Check this host in a terminal with ssh first", stderr.String())
		}
	case <-timer.C:
		return errors.New("SSH setup timed out; check this host in a terminal with ssh first")
	case <-ctx.Done():
		return nil
	}
	// Reuse a CLI-created reverse tunnel after a status check. If its owner
	// exits later, establish our own forward without interrupting SOCKS traffic.
	ensureReverse := func() error {
		if err := probeSOCKS(ctx, port, token, status.SessionID); err == nil {
			return nil
		}
		forwardCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		forward := exec.CommandContext(forwardCtx, "ssh", "-S", controlPath, "-O", "forward",
			"-o", "ExitOnForwardFailure=yes", "-R", "127.0.0.1:9333:127.0.0.1:9333", "--", status.Host)
		if out, err := forward.CombinedOutput(); err != nil {
			return fmt.Errorf("SSH reverse forwarding failed: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		if err := probeSOCKS(ctx, port, token, status.SessionID); err != nil {
			return fmt.Errorf("SSH connected but authenticated forwarding failed: %w", err)
		}
		return nil
	}
	if err := ensureReverse(); err != nil {
		return err
	}
	s.mu.Lock()
	if ctx.Err() != nil || s.status.SessionID != status.SessionID {
		s.mu.Unlock()
		return nil
	}
	s.status.State, s.upstream = "connected", port
	saveActiveHost(status.Host)
	s.mu.Unlock()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-connectionClosed:
			return fmt.Errorf("SSH connection closed: %s", stderr.String())
		case <-ticker.C:
			if err := probeSOCKS(ctx, port, token, status.SessionID); err != nil {
				if err := ensureReverse(); err != nil {
					return fmt.Errorf("SSH forwarding unavailable: %w", err)
				}
			}
		}
	}
}

func reserveSSHPort() (int, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, ln.Close()
}

// Exercise direct-tcpip through SSH to our reverse listener, not just the
// SOCKS greeting. This detects hosts that authenticate but deny TCP forwarding.
func probeSOCKS(ctx context.Context, port int, token, sessionID string) error {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return err
	}
	if greeting != [2]byte{5, 0} {
		return errors.New("SOCKS authentication negotiation failed")
	}
	if _, err := conn.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, 36, 117}); err != nil {
		return err
	}
	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 || reply[3] != 1 {
		return fmt.Errorf("SOCKS forwarding rejected (reply %d)", reply[1])
	}
	if token != "" {
		req, err := protocol.NewRequestWithToken("route-probe", protocol.MethodStatus, protocol.StatusParams{}, 1, sessionID, token)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return err
		}
		var response protocol.Response
		if err := json.NewDecoder(io.LimitReader(conn, 64*1024)).Decode(&response); err != nil {
			return err
		}
		var status protocol.StatusResult
		if response.JSONRPC != "2.0" || response.Error != nil || json.Unmarshal(response.Result, &status) != nil || !status.Connected {
			return errors.New("reverse tunnel is not connected to this workstation's browser")
		}
	}
	return nil
}

type sshErrorBuffer struct {
	mu   sync.Mutex
	text string
}

func (b *sshErrorBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text += string(p)
	if len(b.text) > 4096 {
		b.text = b.text[len(b.text)-4096:]
	}
	return len(p), nil
}

func (b *sshErrorBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.text)
}
