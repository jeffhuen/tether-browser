package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// GetBridgeSocketPath returns the user-scoped Unix domain socket path
// (or named pipe on Windows) used to bridge tether daemon and native host.
func GetBridgeSocketPath() string {
	if env := os.Getenv("TETHER_BRIDGE_SOCKET"); env != "" {
		return env
	}
	if runtime.GOOS == "windows" {
		user := os.Getenv("USERNAME")
		if user == "" {
			user = "default"
		}
		return `\\.\pipe\tether-bridge-` + user
	}
	uid := os.Getuid()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tether-%d", uid))
	return filepath.Join(dir, "bridge.sock")
}

// EnsureBridgeSocketDir creates the 0700 private user directory for the Unix domain socket.
func EnsureBridgeSocketDir(socketPath string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}

// ClearStaleBridgeSocket probes the socket and removes it only if no live host is listening.
func ClearStaleBridgeSocket(socketPath string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return nil
	}
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return errors.New("bridge socket is currently held by an active native host process")
	}
	return os.Remove(socketPath)
}

// ReadNativeMessage reads one length-prefixed message from a native messaging stream (Chrome <-> Host).
// The wire format is a 32-bit little-endian integer followed by JSON bytes.
func ReadNativeMessage(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	msgLen := binary.LittleEndian.Uint32(lenBuf[:])
	if msgLen == 0 {
		return nil, errors.New("empty native message")
	}
	if msgLen > 16*1024*1024 {
		return nil, fmt.Errorf("native message length %d exceeds 16MB limit", msgLen)
	}

	payload := make([]byte, msgLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// WriteNativeMessage writes one length-prefixed message to a native messaging stream.
func WriteNativeMessage(w io.Writer, payload []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ExtensionBridge bridges JSON-RPC requests from local/tunnel clients to the Chrome extension
// over Chrome Native Messaging (stdin/stdout).
type ExtensionBridge struct {
	mu           sync.Mutex
	writeMu      sync.Mutex
	in           io.Reader
	out          io.Writer
	pendingCalls map[string]chan *nativeResponse
	reqCounter   atomic.Uint64
	closed       atomic.Bool
	token        string
}

type nativeRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type nativeResponse struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *nativeError    `json:"error,omitempty"`
}

type nativeError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewExtensionBridge creates a bridge instance operating over the provided native messaging streams.
func NewExtensionBridge(in io.Reader, out io.Writer, token string) *ExtensionBridge {
	return &ExtensionBridge{
		in:           in,
		out:          out,
		pendingCalls: make(map[string]chan *nativeResponse),
		token:        token,
	}
}

// StartReader starts processing inbound messages from Chrome in a background goroutine.
func (b *ExtensionBridge) StartReader(ctx context.Context) {
	go func() {
		for {
			payload, err := ReadNativeMessage(b.in)
			if err != nil {
				if !b.closed.Load() {
					fmt.Fprintf(os.Stderr, "[Tether NativeHost] Stream read error: %v\n", err)
				}
				b.failAllPending(err)
				return
			}

			var resp nativeResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				continue
			}

			if resp.Type == "heartbeat" {
				continue
			}

			if resp.ID != "" {
				b.mu.Lock()
				ch, ok := b.pendingCalls[resp.ID]
				if ok {
					delete(b.pendingCalls, resp.ID)
				}
				b.mu.Unlock()

				if ok && ch != nil {
					ch <- &resp
				}
			}
		}
	}()
}

func (b *ExtensionBridge) failAllPending(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.pendingCalls {
		ch <- &nativeResponse{
			ID: id,
			Error: &nativeError{
				Code:    -32000,
				Message: fmt.Sprintf("native host stream terminated: %v", err),
			},
		}
	}
	b.pendingCalls = make(map[string]chan *nativeResponse)
}

// Call sends a command to the Chrome extension and waits for the response.
func (b *ExtensionBridge) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := fmt.Sprintf("req-%d", b.reqCounter.Add(1))
	reqObj := nativeRequest{
		ID:     id,
		Method: method,
		Params: params,
	}
	payload, err := json.Marshal(reqObj)
	if err != nil {
		return nil, err
	}

	respChan := make(chan *nativeResponse, 1)
	b.mu.Lock()
	b.pendingCalls[id] = respChan
	b.mu.Unlock()

	b.writeMu.Lock()
	err = WriteNativeMessage(b.out, payload)
	b.writeMu.Unlock()
	if err != nil {
		b.mu.Lock()
		delete(b.pendingCalls, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("write native message: %w", err)
	}

	select {
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pendingCalls, id)
		b.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-respChan:
		if resp.Error != nil {
			return nil, fmt.Errorf("extension error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// RunNativeHostServer runs the native messaging host loop. It listens on the private
// Unix domain socket (or named pipe) and proxies incoming JSON-RPC calls to the Chrome extension.
func RunNativeHostServer(ctx context.Context, in io.Reader, out io.Writer) error {
	socketPath := GetBridgeSocketPath()
	if err := EnsureBridgeSocketDir(socketPath); err != nil {
		return fmt.Errorf("ensure socket dir: %w", err)
	}
	if err := ClearStaleBridgeSocket(socketPath); err != nil {
		return fmt.Errorf("clear stale socket: %w", err)
	}

	network := "unix"
	if runtime.GOOS == "windows" {
		network = "tcp" // fallback for Windows socket testability
	}

	ln, err := net.Listen(network, socketPath)
	if err != nil {
		return fmt.Errorf("listen on bridge socket: %w", err)
	}
	defer ln.Close()
	defer os.Remove(socketPath)
	_ = os.Chmod(socketPath, 0600)

	bridge := NewExtensionBridge(in, out, "")
	bridge.StartReader(ctx)

	fmt.Fprintf(os.Stderr, "[Tether NativeHost] Bridge listening on %s\n", socketPath)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handleBridgeConnection(ctx, bridge, conn)
	}
}

func handleBridgeConnection(ctx context.Context, bridge *ExtensionBridge, conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	// Buffer up to 16MB for large DOM / accessibility snapshots
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := bridge.Call(callCtx, req.Method, req.Params)
		cancel()

		var resp protocol.Response
		if err != nil {
			errResp := protocol.NewErrorResponse(req.ID, protocol.CodeInternalError, err.Error(), nil, 0, "")
			resp = *errResp
		} else {
			resp = protocol.Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  res,
			}
		}

		respBytes, _ := json.Marshal(resp)
		respBytes = append(respBytes, '\n')
		if _, writeErr := conn.Write(respBytes); writeErr != nil {
			return
		}
	}
}

// ExtensionDriver implements the BrowserDriver interface by communicating with the
// Tether Chrome Extension over the local bridge socket.
type ExtensionDriver struct {
	socketPath string
	mu         sync.Mutex
	conn       net.Conn
	reader     *bufio.Reader
	reqCounter atomic.Uint64
}

// NewExtensionDriver creates a driver that talks to the extension bridge socket.
func NewExtensionDriver(socketPath string) *ExtensionDriver {
	if socketPath == "" {
		socketPath = GetBridgeSocketPath()
	}
	return &ExtensionDriver{
		socketPath: socketPath,
	}
}

// IsAvailable reports whether the native host bridge socket is currently reachable.
func (d *ExtensionDriver) IsAvailable() bool {
	network := "unix"
	if runtime.GOOS == "windows" {
		network = "tcp"
	}
	conn, err := net.DialTimeout(network, d.socketPath, 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d *ExtensionDriver) ensureConn() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		return nil
	}
	network := "unix"
	if runtime.GOOS == "windows" {
		network = "tcp"
	}
	conn, err := net.DialTimeout(network, d.socketPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to extension bridge at %s: %w (is Chrome open with Tether extension?)", d.socketPath, err)
	}
	d.conn = conn
	d.reader = bufio.NewReaderSize(conn, 1024*1024)
	return nil
}

func (d *ExtensionDriver) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := d.ensureConn(); err != nil {
		return nil, err
	}

	id := fmt.Sprintf("d-%d", d.reqCounter.Add(1))
	req, err := protocol.NewRequest(id, method, params, 0, "")
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.conn.Write(data); err != nil {
		_ = d.conn.Close()
		d.conn = nil
		return nil, fmt.Errorf("write to bridge socket: %w", err)
	}

	line, err := d.reader.ReadBytes('\n')
	if err != nil {
		_ = d.conn.Close()
		d.conn = nil
		return nil, fmt.Errorf("read from bridge socket: %w", err)
	}

	var resp protocol.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal bridge response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("extension error: %s", resp.Error.Message)
	}
	return resp.Result, nil
}

func (d *ExtensionDriver) OpenTab(ctx context.Context, params protocol.OpenParams) (*protocol.OpenResult, error) {
	res, err := d.call(ctx, protocol.MethodOpen, params)
	if err != nil {
		return nil, err
	}
	var out protocol.OpenResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}

func (d *ExtensionDriver) CloseTab(ctx context.Context, params protocol.CloseParams) error {
	_, err := d.call(ctx, protocol.MethodClose, params)
	return err
}

func (d *ExtensionDriver) Snapshot(ctx context.Context, params protocol.SnapshotParams) (*protocol.SnapshotResult, error) {
	res, err := d.call(ctx, protocol.MethodSnapshot, params)
	if err != nil {
		return nil, err
	}
	var out protocol.SnapshotResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}

func (d *ExtensionDriver) Screenshot(ctx context.Context, params protocol.ScreenshotParams) (*protocol.ScreenshotResult, error) {
	res, err := d.call(ctx, protocol.MethodScreenshot, params)
	if err != nil {
		return nil, err
	}
	var out protocol.ScreenshotResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}

func (d *ExtensionDriver) Click(ctx context.Context, params protocol.ClickParams) error {
	_, err := d.call(ctx, protocol.MethodClick, params)
	return err
}
func (d *ExtensionDriver) DblClick(ctx context.Context, params protocol.ClickParams) error {
	_, err := d.call(ctx, protocol.MethodDblClick, params)
	return err
}

func (d *ExtensionDriver) Fill(ctx context.Context, params protocol.FillParams) error {
	_, err := d.call(ctx, protocol.MethodFill, params)
	return err
}

func (d *ExtensionDriver) Type(ctx context.Context, params protocol.TypeParams) error {
	_, err := d.call(ctx, protocol.MethodType, params)
	return err
}

func (d *ExtensionDriver) Press(ctx context.Context, params protocol.PressParams) error {
	_, err := d.call(ctx, protocol.MethodPress, params)
	return err
}

func (d *ExtensionDriver) Hover(ctx context.Context, params protocol.HoverParams) error {
	_, err := d.call(ctx, protocol.MethodHover, params)
	return err
}

func (d *ExtensionDriver) Focus(ctx context.Context, params protocol.FocusParams) error {
	_, err := d.call(ctx, protocol.MethodFocus, params)
	return err
}
func (d *ExtensionDriver) Eval(ctx context.Context, params protocol.EvalParams) (*protocol.EvalResult, error) {
	res, err := d.call(ctx, protocol.MethodEval, params)
	if err != nil {
		return nil, err
	}
	var out protocol.EvalResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}

func (d *ExtensionDriver) Wait(ctx context.Context, params protocol.WaitParams) error {
	_, err := d.call(ctx, protocol.MethodWait, params)
	return err
}

func (d *ExtensionDriver) StartReview(ctx context.Context, params protocol.ReviewParams) error {
	_, err := d.call(ctx, protocol.MethodReviewStart, params)
	return err
}

func (d *ExtensionDriver) GetReviewNotes(ctx context.Context, params protocol.ReviewParams) ([]*protocol.ReviewNote, error) {
	res, err := d.call(ctx, protocol.MethodReviewList, params)
	if err != nil {
		return nil, err
	}
	var out []*protocol.ReviewNote
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return out, nil
}

func (d *ExtensionDriver) ClearReview(ctx context.Context, params protocol.ReviewParams) error {
	_, err := d.call(ctx, protocol.MethodReviewClear, params)
	return err
}
func (d *ExtensionDriver) Status(ctx context.Context, params protocol.StatusParams) (*protocol.StatusResult, error) {
	res, err := d.call(ctx, protocol.MethodStatus, params)
	if err != nil {
		return nil, err
	}
	var out protocol.StatusResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}
func (d *ExtensionDriver) ListTabs(ctx context.Context) (*protocol.TabListResult, error) {
	res, err := d.call(ctx, protocol.MethodTabList, nil)
	if err != nil {
		return nil, err
	}
	var out protocol.TabListResult
	if len(res) > 0 {
		_ = json.Unmarshal(res, &out)
	}
	return &out, nil
}

func (d *ExtensionDriver) SwitchTab(ctx context.Context, params protocol.TabSwitchParams) error {
	_, err := d.call(ctx, protocol.MethodTabSwitch, params)
	return err
}

func (d *ExtensionDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		err := d.conn.Close()
		d.conn = nil
		return err
	}
	return nil
}
