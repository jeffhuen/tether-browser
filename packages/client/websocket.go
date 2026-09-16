package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MaxWebSocketPayload caps the maximum allowed frame/message size (64 MiB).
const MaxWebSocketPayload uint64 = 64 * 1024 * 1024

var (
	ErrWebSocketPayloadTooLarge = errors.New("websocket message exceeds 64 MiB limit")
)

// WSConn represents a minimal raw RFC 6455 WebSocket client connection.
type WSConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	closed  atomic.Bool
}

// DialWebSocket connects to a WebSocket server with context deadline support.
func DialWebSocket(ctx context.Context, endpoint string) (*WSConn, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket URL: %w", err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("dial tcp %s: %w", host, err)
	}

	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	challengeKey := base64.StdEncoding.EncodeToString(keyBytes)

	reqPath := u.Path
	if reqPath == "" {
		reqPath = "/"
	}
	if u.RawQuery != "" {
		reqPath += "?" + u.RawQuery
	}

	handshake := fmt.Sprintf(
		"GET %s HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n\r\n",
		reqPath, u.Host, challengeKey,
	)

	if _, err := conn.Write([]byte(handshake)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read handshake response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("unexpected handshake status: %d %s", resp.StatusCode, resp.Status)
	}

	expectedAccept := computeWebSocketAccept(challengeKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		conn.Close()
		return nil, errors.New("sec-websocket-accept mismatch")
	}

	_ = conn.SetDeadline(time.Time{})

	return &WSConn{
		conn:   conn,
		reader: reader,
	}, nil
}

func computeWebSocketAccept(key string) string {
	const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// WriteTextMessage sends a masked text frame to the server.
func (ws *WSConn) WriteTextMessage(text []byte) error {
	return ws.writeFrame(1, text)
}

func (ws *WSConn) writeFrame(opcode byte, data []byte) error {
	if ws.closed.Load() {
		return errors.New("connection closed")
	}

	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	if ws.closed.Load() {
		return errors.New("connection closed")
	}

	length := len(data)
	var header []byte

	if length <= 125 {
		header = []byte{0x80 | opcode, 0x80 | byte(length)}
	} else if length <= 65535 {
		header = make([]byte, 4)
		header[0] = 0x80 | opcode
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
	} else {
		header = make([]byte, 10)
		header[0] = 0x80 | opcode
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
	}

	maskKey := make([]byte, 4)
	if _, err := rand.Read(maskKey); err != nil {
		return err
	}

	masked := make([]byte, length)
	for i := range data {
		masked[i] = data[i] ^ maskKey[i%4]
	}

	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	if _, err := ws.conn.Write(maskKey); err != nil {
		return err
	}
	_, err := ws.conn.Write(masked)
	return err
}

// ReadMessage reads the next complete message, accumulating continuation frames.
func (ws *WSConn) ReadMessage() (int, []byte, error) {
	var accumulated []byte
	firstOpcode := -1

	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, header); err != nil {
			return 0, nil, err
		}

		fin := (header[0] & 0x80) != 0
		opcode := int(header[0] & 0x0F)
		masked := (header[1] & 0x80) != 0
		rawLen := uint64(header[1] & 0x7F)

		var payloadLen uint64
		if rawLen <= 125 {
			payloadLen = rawLen
		} else if rawLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return 0, nil, err
			}
			payloadLen = uint64(binary.BigEndian.Uint16(ext))
		} else if rawLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return 0, nil, err
			}
			payloadLen = binary.BigEndian.Uint64(ext)
			if payloadLen&(1<<63) != 0 {
				return 0, nil, errors.New("websocket MSB non-zero in 64-bit length")
			}
		}

		if payloadLen > MaxWebSocketPayload || uint64(len(accumulated))+payloadLen > MaxWebSocketPayload {
			return 0, nil, ErrWebSocketPayloadTooLarge
		}

		var maskKey []byte
		if masked {
			maskKey = make([]byte, 4)
			if _, err := io.ReadFull(ws.reader, maskKey); err != nil {
				return 0, nil, err
			}
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(ws.reader, payload); err != nil {
			return 0, nil, err
		}

		if masked {
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}
		}

		switch opcode {
		case 8: // Close
			_ = ws.Close()
			return opcode, nil, io.EOF
		case 9: // Ping
			ws.writeControl(10, payload)
			continue
		case 10: // Pong
			continue
		}

		if firstOpcode == -1 {
			firstOpcode = opcode
		}
		accumulated = append(accumulated, payload...)

		if fin {
			return firstOpcode, accumulated, nil
		}
	}
}

func (ws *WSConn) writeControl(opcode byte, data []byte) {
	if ws.closed.Load() {
		return
	}
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	header := []byte{0x80 | opcode, 0x80 | byte(len(data))}
	maskKey := make([]byte, 4)
	_, _ = rand.Read(maskKey)

	masked := make([]byte, len(data))
	for i := range data {
		masked[i] = data[i] ^ maskKey[i%4]
	}

	_, _ = ws.conn.Write(header)
	_, _ = ws.conn.Write(maskKey)
	_, _ = ws.conn.Write(masked)
}

// Close terminates the WebSocket connection immediately without waiting for write lock.
func (ws *WSConn) Close() error {
	if ws.closed.Swap(true) {
		return nil
	}
	return ws.conn.Close()
}

// CDPClient manages bidirectional JSON-RPC calls over a WebSocket connection to Chrome.
type CDPClient struct {
	ws        *WSConn
	nextID    atomic.Uint64
	mu        sync.Mutex
	pending   map[uint64]chan cdpResult
	listeners []func(method string, params json.RawMessage)
	closed    chan struct{}
}

type cdpResult struct {
	result json.RawMessage
	err    error
}

type cdpIncoming struct {
	ID     *uint64         `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewCDPClient wraps a WebSocket connection with JSON-RPC dispatching.
func NewCDPClient(ws *WSConn) *CDPClient {
	c := &CDPClient{
		ws:      ws,
		pending: make(map[uint64]chan cdpResult),
		closed:  make(chan struct{}),
	}
	go c.readPump()
	return c
}

func (c *CDPClient) readPump() {
	defer func() {
		close(c.closed)
		c.mu.Lock()
		for id, ch := range c.pending {
			ch <- cdpResult{err: errors.New("cdp connection closed")}
			delete(c.pending, id)
		}
		c.mu.Unlock()
	}()

	for {
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			return
		}

		var incoming cdpIncoming
		if err := json.Unmarshal(msg, &incoming); err != nil {
			continue
		}

		if incoming.ID != nil {
			c.mu.Lock()
			ch, exists := c.pending[*incoming.ID]
			if exists {
				delete(c.pending, *incoming.ID)
			}
			c.mu.Unlock()

			if exists {
				var resErr error
				if incoming.Error != nil {
					resErr = fmt.Errorf("cdp error %d: %s", incoming.Error.Code, incoming.Error.Message)
				}
				ch <- cdpResult{result: incoming.Result, err: resErr}
			}
		} else if incoming.Method != "" {
			c.mu.Lock()
			listeners := append([]func(string, json.RawMessage){}, c.listeners...)
			c.mu.Unlock()
			for _, fn := range listeners {
				fn(incoming.Method, incoming.Params)
			}
		}
	}
}

// Call sends a CDP command and waits for its result.
func (c *CDPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		d, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal cdp params: %w", err)
		}
		rawParams = d
	}

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if rawParams != nil {
		req["params"] = rawParams
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal cdp request: %w", err)
	}

	resChan := make(chan cdpResult, 1)
	c.mu.Lock()
	c.pending[id] = resChan
	c.mu.Unlock()

	if err := c.ws.WriteTextMessage(reqBytes); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write cdp message: %w", err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, errors.New("cdp client closed")
	case res := <-resChan:
		return res.result, res.err
	}
}

// Close closes the underlying WebSocket connection.
func (c *CDPClient) Close() error {
	return c.ws.Close()
}
