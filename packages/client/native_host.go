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
	"strings"
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
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "tether", "bridge.sock")
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
	if fi, err := os.Lstat(dir); err == nil {
		// Prevent symlink following or redirection attacks
		if fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(dir)
		}
	}
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
	onClose      func()
	onSystemMsg  func(msgType string, payload []byte) (any, error)
}

func (b *ExtensionBridge) SetOnClose(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onClose = fn
}

func (b *ExtensionBridge) SetOnSystemMessage(fn func(msgType string, payload []byte) (any, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onSystemMsg = fn
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
					fmt.Fprintf(os.Stderr, "[Tether NativeHost] Chrome disconnected: %v\n", err)
				}
				b.failAllPending(err)
				b.mu.Lock()
				onClose := b.onClose
				b.mu.Unlock()
				if onClose != nil {
					onClose()
				}
				return
			}

			var resp nativeResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				continue
			}

			if resp.Type == "heartbeat" {
				continue
			}

			if strings.HasPrefix(resp.Type, "system_") {
				b.mu.Lock()
				onSys := b.onSystemMsg
				b.mu.Unlock()
				if onSys != nil {
					go func(msgType, reqID string, p []byte) {
						res, err := onSys(msgType, p)
						var outResp nativeResponse
						if err != nil {
							outResp = nativeResponse{
								ID:    reqID,
								Type:  "response",
								Error: &nativeError{Code: -32000, Message: err.Error()},
							}
						} else {
							resBytes, _ := json.Marshal(res)
							outResp = nativeResponse{
								ID:     reqID,
								Type:   "response",
								Result: resBytes,
							}
						}
						outBytes, _ := json.Marshal(outResp)
						b.writeMu.Lock()
						_ = WriteNativeMessage(b.out, outBytes)
						b.writeMu.Unlock()
					}(resp.Type, resp.ID, payload)
				}
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
func RunNativeHostServer(ctx context.Context, in io.Reader, out io.Writer, onSystemMsg func(string, []byte) (any, error)) error {
	socketPath := GetBridgeSocketPath()
	if err := EnsureBridgeSocketDir(socketPath); err != nil {
		return fmt.Errorf("ensure socket dir: %w", err)
	}
	if err := ClearStaleBridgeSocket(socketPath); err != nil {
		return fmt.Errorf("clear stale socket: %w", err)
	}

	network := "unix"
	if runtime.GOOS == "windows" {
		network = "tcp"
	}

	ln, err := net.Listen(network, socketPath)
	if err != nil {
		return fmt.Errorf("listen on bridge socket: %w", err)
	}
	defer ln.Close()
	defer os.Remove(socketPath)
	_ = os.Chmod(socketPath, 0600)

	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	bridge := NewExtensionBridge(in, out, "")
	bridge.SetOnSystemMessage(onSystemMsg)
	bridge.SetOnClose(func() {
		cancel()
		_ = ln.Close()
	})
	bridge.StartReader(serverCtx)

	fmt.Fprintf(os.Stderr, "[Tether NativeHost] Bridge listening on %s\n", socketPath)

	go func() {
		<-serverCtx.Done()
		_ = ln.Close()
	}()

	var activeClients atomic.Int32
	notifyClientCount := func(count int32) {
		statusPayload, _ := json.Marshal(map[string]any{
			"type":        "bridge_status",
			"clientCount": count,
		})
		bridge.writeMu.Lock()
		_ = WriteNativeMessage(bridge.out, statusPayload)
		bridge.writeMu.Unlock()
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if serverCtx.Err() != nil {
				return nil
			}
			return err
		}
		active := activeClients.Add(1)
		notifyClientCount(active)
		go func(c net.Conn) {
			handleBridgeConnection(serverCtx, bridge, c)
			remaining := activeClients.Add(-1)
			notifyClientCount(remaining)
		}(conn)
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

func (d *ExtensionDriver) dialConnLocked() error {
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

func (d *ExtensionDriver) ensureConn() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		return nil
	}
	return d.dialConnLocked()
}

func (d *ExtensionDriver) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
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

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if d.conn == nil {
			if err := d.dialConnLocked(); err != nil {
				return nil, err
			}
		}

		if _, err := d.conn.Write(data); err != nil {
			_ = d.conn.Close()
			d.conn = nil
			lastErr = err
			continue
		}

		line, err := d.reader.ReadBytes('\n')
		if err != nil {
			_ = d.conn.Close()
			d.conn = nil
			lastErr = err
			continue
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

	return nil, fmt.Errorf("bridge communication failed after retry: %w", lastErr)
}

func extensionCall[T any](ctx context.Context, d *ExtensionDriver, method string, params any) (*T, error) {
	res, err := d.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("%s result: %w", method, err)
	}
	return &out, nil
}

func (d *ExtensionDriver) OpenTab(ctx context.Context, params protocol.OpenParams) (*protocol.OpenResult, error) {
	return extensionCall[protocol.OpenResult](ctx, d, protocol.MethodOpen, params)
}

func (d *ExtensionDriver) CloseTab(ctx context.Context, params protocol.CloseParams) error {
	_, err := d.call(ctx, protocol.MethodClose, params)
	return err
}

func (d *ExtensionDriver) Snapshot(ctx context.Context, params protocol.SnapshotParams) (*protocol.SnapshotResult, error) {
	// CDP accessibility values can be numbers or booleans, while AXNode uses text.
	out, err := extensionCall[struct {
		protocol.SnapshotResult
		Nodes []struct {
			protocol.AXNode
			Value any `json:"value"`
		} `json:"nodes"`
	}](ctx, d, protocol.MethodSnapshot, params)
	if err != nil {
		return nil, err
	}
	if out.Nodes != nil {
		out.SnapshotResult.Nodes = make([]*protocol.AXNode, len(out.Nodes))
	}
	for i := range out.Nodes {
		node := &out.Nodes[i]
		switch value := node.Value.(type) {
		case nil:
		case string:
			node.AXNode.Value = value
		case float64, bool:
			node.AXNode.Value = fmt.Sprint(value)
		default:
			return nil, fmt.Errorf("snapshot node %d has invalid accessibility value %T", i, value)
		}
		out.SnapshotResult.Nodes[i] = &node.AXNode
	}
	return &out.SnapshotResult, nil
}

func (d *ExtensionDriver) Screenshot(ctx context.Context, params protocol.ScreenshotParams) (*protocol.ScreenshotResult, error) {
	return extensionCall[protocol.ScreenshotResult](ctx, d, protocol.MethodScreenshot, params)
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

func (d *ExtensionDriver) Scroll(ctx context.Context, params protocol.ScrollParams) error {
	_, err := d.call(ctx, protocol.MethodScroll, params)
	return err
}
func (d *ExtensionDriver) Eval(ctx context.Context, params protocol.EvalParams) (*protocol.EvalResult, error) {
	return extensionCall[protocol.EvalResult](ctx, d, protocol.MethodEval, params)
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
	out, err := extensionCall[protocol.ReviewListResult](ctx, d, protocol.MethodReviewList, params)
	if err != nil {
		return nil, err
	}
	return out.Notes, nil
}

func (d *ExtensionDriver) ClearReview(ctx context.Context, params protocol.ReviewParams) error {
	_, err := d.call(ctx, protocol.MethodReviewClear, params)
	return err
}
func (d *ExtensionDriver) Status(ctx context.Context, params protocol.StatusParams) (*protocol.StatusResult, error) {
	return extensionCall[protocol.StatusResult](ctx, d, protocol.MethodStatus, params)
}
func (d *ExtensionDriver) ListTabs(ctx context.Context) (*protocol.TabListResult, error) {
	return extensionCall[protocol.TabListResult](ctx, d, protocol.MethodTabList, nil)
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
