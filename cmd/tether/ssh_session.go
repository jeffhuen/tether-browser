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
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

type sshConnectionStatus struct {
	State              string `json:"state"`
	Host               string `json:"host"`
	ProxyPort          int    `json:"proxyPort"`
	ProxyToken         string `json:"proxyToken,omitempty"`
	ProxyRealm         string `json:"proxyRealm,omitempty"`
	ProxyAuthSupported bool   `json:"proxyAuthSupported"`
	SessionID          string `json:"sessionId"`
	Error              string `json:"error,omitempty"`
	// SignInURL is a pending Tailscale SSH check-mode sign-in for this attempt.
	SignInURL string `json:"signInUrl,omitempty"`
	// SignInRequired reports that the session stopped because sign-in was not completed.
	SignInRequired bool   `json:"signInRequired,omitempty"`
	AuthProvider   string `json:"authProvider,omitempty"`
	AuthMessage    string `json:"authMessage,omitempty"`
}

// Cellular and relayed Tailscale links stall for seconds at a time. A stall
// must not tear down agent access, so probes are slow to give up and a
// session that was working reconnects on its own until Disconnect.
const (
	probeTimeout      = 10 * time.Second
	probeInterval     = 15 * time.Second
	probeFailureLimit = 3
)

var (
	runSSH            = (*sshSession).run // replaced in tests
	reconnectDelay    = 2 * time.Second
	maxReconnectDelay = 30 * time.Second
	setupTimeout      = 30 * time.Second
	// Tailscale SSH check mode holds the connection until the user signs in.
	signInTimeout = 5 * time.Minute
)

// Tailscale SSH check mode prints this link and waits; it never opens a browser.
var tailscaleSignIn = regexp.MustCompile(`https://login\.tailscale\.com/a/[0-9A-Za-z]+`)

var (
	errSetupTimeout  = errors.New("SSH setup timed out")
	errSignInTimeout = errors.New("SSH sign-in was not completed in time")
)

// signInURL returns the last Tailscale sign-in link in text, or "".
func signInURL(text string) string {
	found := tailscaleSignIn.FindAllString(text, -1)
	if len(found) == 0 {
		return ""
	}
	return found[len(found)-1]
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
	upstream string
	wake     chan struct{} // cuts a reconnect wait short
	// signedIn: the current attempt passed SSH authentication, so a late
	// sign-in banner must not mark it as waiting for sign-in again.
	signedIn    bool
	closed      bool
	controlPath string
	httpProxy   *httputil.ReverseProxy
	proxyServer *http.Server
	proxyToken  string
	proxyRealm  string
}

func (s *sshSession) Status() sshConnectionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	status.ProxyAuthSupported = true
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
		// A dropped session keeps its retry loop; an explicit Connect retries now.
		if s.status.State == "connecting" && s.status.Error != "" {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
		return s.status, nil
	}
	if s.listener == nil {
		var credential [48]byte
		if _, err := rand.Read(credential[:]); err != nil {
			return sshConnectionStatus{}, err
		}
		ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return sshConnectionStatus{}, fmt.Errorf("reserve browser proxy: %w; turn remote browsing off, reconnect, then enable it again", err)
		}
		s.listener = ln
		s.proxyToken = hex.EncodeToString(credential[:32])
		s.proxyRealm = "tether-" + hex.EncodeToString(credential[32:])
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
	s.ctx, s.cancel, s.upstream, s.httpProxy = ctx, cancel, "", nil
	s.done, s.wake = make(chan struct{}), make(chan struct{}, 1)
	wake := s.wake
	s.status = sshConnectionStatus{
		State: "connecting", Host: host,
		ProxyPort:  s.listener.Addr().(*net.TCPAddr).Port,
		SessionID:  hex.EncodeToString(nonce[:]),
		ProxyToken: s.proxyToken, ProxyRealm: s.proxyRealm,
		ProxyAuthSupported: true,
	}
	status := s.status
	go func(done chan struct{}) {
		defer close(done)
		defer cancel()
		// Reap the previous SSH child before competing for the remote reverse port.
		if previousDone != nil {
			<-previousDone
		}
		delay := reconnectDelay
		for everConnected := false; ctx.Err() == nil; {
			s.mu.Lock()
			s.signedIn = false
			s.mu.Unlock()
			err := runSSH(s, ctx, status)
			s.mu.Lock()
			// Disconnect, Close, or a newer Connect already owns the status.
			if ctx.Err() != nil || s.status.SessionID != status.SessionID {
				s.mu.Unlock()
				return
			}
			up := s.upstream != "" // run sets upstream only after forwarding was verified
			everConnected = everConnected || up
			// The link belongs to the finished attempt; Tailscale issues a new one next time.
			needSignIn := errors.Is(err, errSignInTimeout)
			authStopped := !s.signedIn && (s.status.AuthProvider != "" || s.status.SignInURL != "")
			s.upstream, s.controlPath, s.status.SignInURL, s.httpProxy = "", "", "", nil
			if err != nil {
				s.status.Error = err.Error()
			}
			// Retrying would only print links nobody is watching; wait for Reconnect.
			if !everConnected || needSignIn || authStopped {
				// Never came up: a bad host, sign-in, or denied forwarding will not fix itself.
				s.status.State, s.status.SignInRequired = "disconnected", needSignIn
				if authStopped && s.status.AuthProvider == "NetBird" {
					if needSignIn {
						s.status.AuthMessage = "NetBird SSH authentication timed out. Reconnect to try again."
					} else {
						s.status.AuthMessage = "NetBird SSH authentication failed. Reconnect to try again."
					}
				}
				if getActiveHost() == host {
					clearActiveHost()
				}
				s.mu.Unlock()
				return
			}
			s.status.State = "connecting"
			s.status.AuthProvider, s.status.AuthMessage = "", ""
			s.mu.Unlock()
			if up {
				delay = reconnectDelay
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-time.After(delay):
			}
			delay = min(delay*2, maxReconnectDelay)
		}
	}(s.done)
	return status, nil
}

// setSignInURL records a sign-in link for the attempt still connecting under
// sessionID. It reports whether the link is new.
func (s *sshSession) setSignInURL(sessionID, url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signedIn || s.status.SessionID != sessionID || s.status.State != "connecting" || s.status.SignInURL == url {
		return false
	}
	s.status.SignInURL = url
	return true
}

func (s *sshSession) setAuthWait(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signedIn || s.status.SessionID != sessionID || s.status.State != "connecting" || s.status.AuthProvider != "" {
		return false
	}
	s.status.AuthProvider, s.status.AuthMessage = "NetBird", "Waiting for NetBird SSH authentication. NetBird opens its sign-in browser."
	return true
}

// completeSignIn records that the current attempt passed SSH authentication.
// A later forwarding failure then retries instead of asking to sign in again.
func (s *sshSession) completeSignIn(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.SessionID == sessionID {
		s.signedIn, s.status.SignInURL = true, ""
		s.status.AuthProvider, s.status.AuthMessage = "", ""
	}
}

func (s *sshSession) Disconnect() sshConnectionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.ProxyAuthSupported = true
	if s.cancel != nil {
		s.cancel()
	}
	if getActiveHost() == s.status.Host {
		clearActiveHost()
	}
	s.status.State, s.status.SessionID, s.status.Error, s.upstream = "disconnected", "", "", ""
	s.httpProxy = nil
	s.status.SignInURL, s.status.SignInRequired = "", false
	s.controlPath, s.status.AuthProvider, s.status.AuthMessage = "", "", ""
	return s.status
}

func (s *sshSession) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.status.State, s.status.SignInURL, s.upstream = "disconnected", "", ""
	s.httpProxy = nil
	s.controlPath, s.status.AuthProvider, s.status.AuthMessage = "", "", ""
	if s.status.Host != "" && getActiveHost() == s.status.Host {
		clearActiveHost()
	}
	ln, done, server := s.listener, s.done, s.proxyServer
	s.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if ln != nil {
		_ = ln.Close()
	}
	if done != nil {
		<-done
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
	controlDir, err := os.MkdirTemp("", "tether-ssh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(controlDir)
	controlPath := filepath.Join(controlDir, "s")
	socketPath := filepath.Join(controlDir, "p")
	// A held stdin keeps the remote cat alive. Its random readiness marker is
	// emitted only after SSH authentication and token sync.
	remote := cli.TokenSyncCommand() + " && printf '%s\\n' " + cli.ShellQuote(status.SessionID) + " && cat"
	netbird := usesNetBirdProxy(ctx, status.Host, nil)
	connectTimeout := "10"
	if netbird {
		connectTimeout = "300"
	}
	args := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + connectTimeout, "-o", "LogLevel=INFO",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4",
		"-o", "ExitOnForwardFailure=yes", "-o", "ForkAfterAuthentication=no",
		"-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-S", controlPath, "-o", "SessionType=default",
		"-o", "ClearAllForwardings=yes", "-o", "StreamLocalBindMask=0177",
		"--", status.Host, remote,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cli.ManageCommand(cmd)
	// The Tailscale sign-in link can arrive as an SSH banner (stderr) or as session output.
	signIn := make(chan struct{}, 1)
	noteSignIn := func(url string) {
		if url != "" && s.setSignInURL(status.SessionID, url) {
			select {
			case signIn <- struct{}{}:
			default:
			}
		}
	}
	stderr := &sshErrorBuffer{onSignIn: noteSignIn}
	if netbird {
		stderr.onAuthWait = func() {
			if s.setAuthWait(status.SessionID) {
				select {
				case signIn <- struct{}{}:
				default:
				}
			}
		}
		stderr.onAuthFailure = func() { s.setAuthWait(status.SessionID) }
	}
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
		_ = cli.KillCommand(cmd)
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
			noteSignIn(signInURL(scanner.Text()))
			if netbird && (strings.Contains(scanner.Text(), "SSH authentication required.") || strings.Contains(scanner.Text(), "Waiting for authentication...")) {
				stderr.onAuthWait()
			}
		}
		ready <- false
	}()
	switch ok, err := waitReady(ctx, ready, signIn); {
	case ctx.Err() != nil:
		return nil
	case err != nil:
		return fmt.Errorf("%w%s. Check this host in a terminal with ssh first", err, sshDetail(stderr))
	case !ok:
		return fmt.Errorf("SSH could not connect%s. Check this host in a terminal with ssh first", sshDetail(stderr))
	}
	s.completeSignIn(status.SessionID)
	closeProxy, err := openPrivateSSHProxy(ctx, controlPath, status.Host, socketPath)
	if err != nil {
		return err
	}
	defer closeProxy()
	// Reuse a CLI-created reverse tunnel after a status check. If its owner
	// exits later, establish our own forward without interrupting SOCKS traffic.
	ensureReverse := func() error {
		return ensureSSHReverse(ctx, controlPath, status.Host, socketPath, token, status.SessionID)
	}
	if err := ensureReverse(); err != nil {
		return err
	}
	proxy, transport := newSSHHTTPProxy(ctx, socketPath)
	defer transport.CloseIdleConnections()
	s.mu.Lock()
	if ctx.Err() != nil || s.status.SessionID != status.SessionID {
		s.mu.Unlock()
		return nil
	}
	s.status.State, s.status.Error, s.status.SignInURL, s.upstream = "connected", "", "", socketPath
	s.httpProxy = proxy
	s.controlPath = controlPath
	saveActiveHost(status.Host)
	s.mu.Unlock()
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	for failures := 0; ; {
		select {
		case <-ctx.Done():
			return nil
		case <-connectionClosed:
			return fmt.Errorf("SSH connection closed: %s", stderr.String())
		case <-ticker.C:
			if err := ensureReverse(); err == nil {
				failures = 0
			} else if failures++; failures >= probeFailureLimit {
				return fmt.Errorf("SSH forwarding unavailable: %w", err)
			}
		}
	}
}

// Exercise direct-tcpip through SSH to our reverse listener, not just the
// SOCKS greeting. This detects hosts that authenticate but deny TCP forwarding.
func probeSOCKS(ctx context.Context, socketPath, token, sessionID string) error {
	conn, err := dialSSHProxy(ctx, socketPath, "127.0.0.1:9333")
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(probeTimeout))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	secured, err := protocol.AuthenticateDaemon(ctx, conn, token)
	if err != nil {
		return err
	}
	req, err := protocol.NewRequest("route-probe", protocol.MethodStatus, protocol.StatusParams{}, 1, sessionID)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(secured).Encode(req); err != nil {
		return err
	}
	response, err := protocol.ReadResponse(secured, req)
	if err != nil {
		return err
	}
	var status protocol.StatusResult
	if response.Error != nil || json.Unmarshal(response.Result, &status) != nil || !status.Connected {
		return errors.New("reverse tunnel is not connected to this workstation's browser")
	}
	return nil
}

// waitReady waits for the SSH readiness marker. The first pending Tailscale
// sign-in replaces the setup timeout with signInTimeout, once per attempt.
func waitReady(ctx context.Context, ready <-chan bool, signIn <-chan struct{}) (bool, error) {
	timer := time.NewTimer(setupTimeout)
	defer timer.Stop()
	timeout := errSetupTimeout
	for {
		select {
		case ok := <-ready:
			return ok, nil
		case <-signIn:
			// Extend once: a server that keeps printing new links must not stall forever.
			if timeout == errSetupTimeout {
				timer.Reset(signInTimeout)
				timeout = errSignInTimeout
			}
		case <-timer.C:
			return false, timeout
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// sshDetail formats what SSH printed as ": text", or "" when it printed nothing.
func sshDetail(b *sshErrorBuffer) string {
	if text := b.String(); text != "" {
		return ": " + text
	}
	return ""
}

type sshErrorBuffer struct {
	mu            sync.Mutex
	text          string
	pending       string
	signIn        string
	onSignIn      func(string)
	onAuthWait    func()
	onAuthFailure func()
}

func (b *sshErrorBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	combined := b.pending + string(p)
	end := strings.LastIndexByte(combined, '\n') + 1
	complete := combined[:end]
	b.pending = combined[end:]
	// Match the stdout scanner's bounded line size, independently of the
	// 4096-byte diagnostic tail, so long split lines are not lost.
	if len(b.pending) > 64<<10 {
		b.pending = b.pending[len(b.pending)-4096:]
	}
	b.text += string(p)
	url := signInURL(complete)
	authWait := strings.Contains(complete, "SSH authentication required.") || strings.Contains(complete, "Waiting for authentication...")
	authFailure := netBirdAuthFailure(complete)
	if len(b.text) > 4096 {
		b.text = b.text[len(b.text)-4096:]
	}
	fresh := url != "" && url != b.signIn
	if fresh {
		b.signIn = url
	}
	b.mu.Unlock()
	if fresh && b.onSignIn != nil {
		b.onSignIn(url)
	}
	if authWait && b.onAuthWait != nil {
		b.onAuthWait()
	}
	if authFailure && b.onAuthFailure != nil {
		b.onAuthFailure()
	}
	return len(p), nil
}

func (b *sshErrorBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.text)
}
