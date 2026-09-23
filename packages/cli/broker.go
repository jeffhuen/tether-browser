package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// DefaultBrokerSocket returns the default unix socket path for the session broker.
func DefaultBrokerSocket() string {
	if sock := os.Getenv("TETHER_BROKER_SOCKET"); sock != "" {
		return sock
	}
	uid := os.Getuid()
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		// Shells without XDG_RUNTIME_DIR must find the same broker as login shells.
		if fi, err := os.Stat(fmt.Sprintf("/run/user/%d", uid)); err == nil && fi.IsDir() {
			runtimeDir = fmt.Sprintf("/run/user/%d", uid)
		}
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tether-%d", uid))
	if runtimeDir != "" {
		dir = filepath.Join(runtimeDir, "tether")
	}
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "broker.sock")
}

// Broker manages a persistent connection to the tether daemon and brokers CLI requests.
type Broker struct {
	socketPath     string
	daemonAddr     string
	listener       net.Listener
	client         *Client
	sessionTargets map[string]protocol.TargetID
	mu             sync.Mutex
	shutdownChan   chan struct{}
}

// NewBroker creates a Broker instance.
func NewBroker(socketPath, daemonAddr string) *Broker {
	if socketPath == "" {
		socketPath = DefaultBrokerSocket()
	}
	if daemonAddr == "" {
		daemonAddr = DefaultDaemonAddr
	}
	return &Broker{
		socketPath:     socketPath,
		daemonAddr:     daemonAddr,
		client:         NewClient(daemonAddr),
		sessionTargets: make(map[string]protocol.TargetID),
		shutdownChan:   make(chan struct{}),
	}
}

// IsBrokerAlive checks whether the broker unix socket is responding.
func IsBrokerAlive(socketPath string) bool {
	if socketPath == "" {
		socketPath = DefaultBrokerSocket()
	}
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Run starts the broker socket listener in the foreground and serves requests until stopped.
func (b *Broker) Run(ctx context.Context) error {
	if fi, err := os.Lstat(b.socketPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked broker socket: %s", b.socketPath)
		}
		if IsBrokerAlive(b.socketPath) {
			return fmt.Errorf("broker already running on %s", b.socketPath)
		}
		_ = os.Remove(b.socketPath)
	}

	dir := filepath.Dir(b.socketPath)
	if err := os.MkdirAll(dir, 0700); err == nil {
		_ = os.Chmod(dir, 0700)
	}

	oldUmask := setRestrictiveUmask()
	listener, err := net.Listen("unix", b.socketPath)
	restoreUmask(oldUmask)
	if err != nil {
		return fmt.Errorf("listen on unix socket %s: %w", b.socketPath, err)
	}
	_ = os.Chmod(b.socketPath, 0600)
	b.mu.Lock()
	b.listener = listener
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.listener = nil
		b.mu.Unlock()
		_ = listener.Close()
		_ = os.Remove(b.socketPath)
	}()
	errChan := make(chan error, 1)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-b.shutdownChan:
					errChan <- nil
					return
				case <-ctx.Done():
					errChan <- ctx.Err()
					return
				default:
					errChan <- err
					return
				}
			}
			go b.handleClient(conn)
		}
	}()

	select {
	case <-ctx.Done():
		b.Close()
		return ctx.Err()
	case <-b.shutdownChan:
		return nil
	case err := <-errChan:
		return err
	}
}

// Close closes the broker listener and releases resources.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	select {
	case <-b.shutdownChan:
	default:
		close(b.shutdownChan)
	}
	if b.listener != nil {
		_ = b.listener.Close()
		b.listener = nil
	}
}

// handleClient handles an incoming CLI command connection over the unix socket.
func (b *Broker) handleClient(conn net.Conn) {
	defer conn.Close()

	if err := verifyPeerCredentials(conn); err != nil {
		log.Printf("broker: rejected connection: %v", err)
		return
	}

	decoder := json.NewDecoder(io.LimitReader(conn, 16*1024*1024))
	var req protocol.Request
	if err := decoder.Decode(&req); err != nil {
		return
	}

	// Graceful remote shutdown command
	if req.Method == "broker.shutdown" {
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		data, _ := json.Marshal(resp)
		_, _ = conn.Write(append(data, '\n'))
		b.Close()
		return
	}

	sessionKey := b.extractSessionKey(req.Params)

	// Single atomic lookup and validation of session target
	var targetToInject protocol.TargetID
	b.mu.Lock()
	if sessionKey != "default" && sessionKey != "" {
		tid, exists := b.sessionTargets[sessionKey]
		if (!exists || tid == "") && req.Method != protocol.MethodOpen && req.Method != protocol.MethodStatus {
			b.mu.Unlock()
			errResp := protocol.NewErrorResponse(req.ID, protocol.CodeTargetNotFound, fmt.Sprintf("session %q has no active target", sessionKey), nil, b.client.NextSeq(), b.client.Epoch())
			data, _ := json.Marshal(errResp)
			_, _ = conn.Write(append(data, '\n'))
			return
		}
		targetToInject = tid
	} else {
		targetToInject = b.sessionTargets["default"]
	}
	b.mu.Unlock()

	// Inject target ID into params
	modifiedParams := b.injectTarget(targetToInject, req.Method, req.Params)

	// Derive timeout from request parameters
	timeout := 60 * time.Second
	if t := extractTimeout(modifiedParams); t > 0 {
		timeout = t
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resp, err := b.client.Call(ctx, req.Method, modifiedParams)
	if err != nil {
		errResp := protocol.NewErrorResponse(req.ID, protocol.CodeInternalError, err.Error(), nil, b.client.NextSeq(), b.client.Epoch())
		data, _ := json.Marshal(errResp)
		_, _ = conn.Write(append(data, '\n'))
		return
	}

	// Update session-specific active target tracking on open or close
	b.updateSessionState(sessionKey, req.Method, resp)

	// Preserve the caller's request ID
	resp.ID = req.ID

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}

func (b *Broker) extractSessionKey(rawParams json.RawMessage) string {
	if len(rawParams) == 0 {
		return "default"
	}
	var m map[string]any
	if err := json.Unmarshal(rawParams, &m); err == nil {
		if s, ok := m["session"].(string); ok && s != "" {
			return s
		}
	}
	return "default"
}

func extractTimeout(params any) time.Duration {
	if params == nil {
		return 0
	}
	if m, ok := params.(map[string]any); ok {
		if ms, ok := m["timeoutMs"].(float64); ok && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
		if ms, ok := m["timeoutMs"].(int); ok && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	if raw, ok := params.(json.RawMessage); ok && len(raw) > 0 {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil {
			if ms, ok := m["timeoutMs"].(float64); ok && ms > 0 {
				return time.Duration(ms) * time.Millisecond
			}
			if ms, ok := m["timeoutMs"].(int); ok && ms > 0 {
				return time.Duration(ms) * time.Millisecond
			}
		}
	}
	return 0
}

func (b *Broker) injectTarget(target protocol.TargetID, method string, rawParams json.RawMessage) any {
	if target == "" || method == protocol.MethodOpen || method == protocol.MethodStatus {
		return rawParams
	}

	var m map[string]any
	if len(rawParams) > 0 {
		_ = json.Unmarshal(rawParams, &m)
	}
	if m == nil {
		m = make(map[string]any)
	}

	if tid, exists := m["targetId"]; !exists || tid == "" {
		m["targetId"] = string(target)
	}
	return m
}

func (b *Broker) updateSessionState(sessionKey string, method string, resp *protocol.Response) {
	if resp == nil || resp.Error != nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch method {
	case protocol.MethodOpen:
		var res protocol.OpenResult
		if err := resp.UnmarshalResult(&res); err == nil && res.TargetID != "" {
			b.sessionTargets[sessionKey] = res.TargetID
		}
	case protocol.MethodClose:
		delete(b.sessionTargets, sessionKey)
	}
}

// StartBackgroundBroker launches the broker as a background process.
func StartBackgroundBroker() error {
	socketPath := DefaultBrokerSocket()
	if IsBrokerAlive(socketPath) {
		return nil
	}

	bin, err := os.Executable()
	if err != nil {
		bin = "tether"
	}

	cmd := exec.Command(bin, "broker", "run")
	setSysProcAttr(cmd)

	logPath := filepath.Join(os.TempDir(), "tether-broker.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start broker background process: %w", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if IsBrokerAlive(socketPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return errors.New("timed out waiting for broker process to start")
}

// StopBroker sends a shutdown request over the socket and waits for the broker process to exit.
func StopBroker() error {
	socketPath := DefaultBrokerSocket()
	if !IsBrokerAlive(socketPath) {
		_ = os.Remove(socketPath)
		return nil
	}

	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		_ = os.Remove(socketPath)
		return nil
	}
	defer conn.Close()

	req, _ := protocol.NewRequest("shutdown", "broker.shutdown", nil, 0, "")
	reqBytes, _ := json.Marshal(req)
	_, _ = conn.Write(append(reqBytes, '\n'))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !IsBrokerAlive(socketPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = os.Remove(socketPath)
	return nil
}

// HandleBrokerCommand executes broker subcommands (run, start, stop, status).
func HandleBrokerCommand(subcmd string, stdout, stderr io.Writer) int {
	socketPath := DefaultBrokerSocket()
	daemonAddr := os.Getenv("TETHER_DAEMON_ADDR")
	if daemonAddr == "" {
		daemonAddr = DefaultDaemonAddr
	}

	switch subcmd {
	case "run":
		broker := NewBroker(socketPath, daemonAddr)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		fmt.Fprintf(stdout, "Broker listening on %s\n", socketPath)
		if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(stderr, "Broker error: %v\n", err)
			return 1
		}
		return 0

	case "start":
		if IsBrokerAlive(socketPath) {
			fmt.Fprintln(stdout, "Broker already running")
			return 0
		}
		if err := StartBackgroundBroker(); err != nil {
			fmt.Fprintf(stderr, "Failed to start broker: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Broker started successfully")
		return 0

	case "stop":
		if err := StopBroker(); err != nil {
			fmt.Fprintf(stderr, "Failed to stop broker: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Broker stopped")
		return 0

	case "status":
		if IsBrokerAlive(socketPath) {
			fmt.Fprintln(stdout, "Broker status: running")
		} else {
			fmt.Fprintln(stdout, "Broker status: stopped")
		}
		return 0

	default:
		fmt.Fprintf(stderr, "Unknown broker subcommand: %s (valid: run, start, stop, status)\n", subcmd)
		return 1
	}
}
