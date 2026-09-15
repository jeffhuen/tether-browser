package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// WSConn represents a low-level RFC 6455 WebSocket connection.
type WSConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	closed  atomic.Bool
}

// DialWebSocket connects to a WebSocket endpoint and completes the handshake.
func DialWebSocket(ctx context.Context, rawURL string) (*WSConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket url: %w", err)
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
		return nil, fmt.Errorf("dial websocket tcp: %w", err)
	}

	reqURI := u.RequestURI()
	if reqURI == "" {
		reqURI = "/"
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	secKey := base64.StdEncoding.EncodeToString(keyBytes)

	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		reqURI, u.Host, secKey)

	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read handshake status: %w", err)
	}

	if !strings.Contains(statusLine, "101") {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake failed with status: %s", strings.TrimSpace(statusLine))
	}

	// Consume remaining HTTP headers until empty line
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("read handshake headers: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	return &WSConn{
		conn:   conn,
		reader: reader,
	}, nil
}

// WriteTextMessage sends a masked text frame (client to server).
func (ws *WSConn) WriteTextMessage(data []byte) error {
	if ws.closed.Load() {
		return errors.New("websocket connection closed")
	}

	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	length := len(data)
	var header []byte
	if length < 126 {
		header = make([]byte, 2)
		header[0] = 0x81 // FIN | Text
		header[1] = 0x80 | byte(length)
	} else if length <= 65535 {
		header = make([]byte, 4)
		header[0] = 0x81
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
	} else {
		header = make([]byte, 10)
		header[0] = 0x81
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
	}

	maskKey := make([]byte, 4)
	if _, err := rand.Read(maskKey); err != nil {
		return err
	}

	masked := make([]byte, length)
	for i := range length {
		masked[i] = data[i] ^ maskKey[i%4]
	}

	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	if _, err := ws.conn.Write(maskKey); err != nil {
		return err
	}
	if _, err := ws.conn.Write(masked); err != nil {
		return err
	}
	return nil
}

// ReadMessage reads the next WebSocket message, responding to pings automatically.
func (ws *WSConn) ReadMessage() (int, []byte, error) {
	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.reader, header); err != nil {
			return 0, nil, err
		}

		opcode := int(header[0] & 0x0F)
		masked := (header[1] & 0x80) != 0
		payloadLen := uint64(header[1] & 0x7F)

		if payloadLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return 0, nil, err
			}
			payloadLen = uint64(binary.BigEndian.Uint16(ext))
		} else if payloadLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(ws.reader, ext); err != nil {
				return 0, nil, err
			}
			payloadLen = binary.BigEndian.Uint64(ext)
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
			for i := uint64(0); i < payloadLen; i++ {
				payload[i] ^= maskKey[i%4]
			}
		}

		switch opcode {
		case 8: // Close
			_ = ws.Close()
			return opcode, nil, io.EOF
		case 9: // Ping
			ws.writeControl(10, payload) // Respond with Pong
			continue
		case 10: // Pong
			continue
		default:
			return opcode, payload, nil
		}
	}
}

func (ws *WSConn) writeControl(opcode byte, data []byte) {
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

// Close terminates the underlying network connection.
func (ws *WSConn) Close() error {
	if ws.closed.CompareAndSwap(false, true) {
		return ws.conn.Close()
	}
	return nil
}

// CDPClient coordinates JSON-RPC communication over a WebSocket connection.
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
		_, payload, err := c.ws.ReadMessage()
		if err != nil {
			return
		}

		var in cdpIncoming
		if err := json.Unmarshal(payload, &in); err != nil {
			continue
		}

		if in.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*in.ID]
			if ok {
				delete(c.pending, *in.ID)
			}
			c.mu.Unlock()

			if ok {
				if in.Error != nil {
					ch <- cdpResult{err: fmt.Errorf("cdp error %d: %s", in.Error.Code, in.Error.Message)}
				} else {
					ch <- cdpResult{result: in.Result}
				}
			}
		} else if in.Method != "" {
			c.mu.Lock()
			listeners := make([]func(string, json.RawMessage), len(c.listeners))
			copy(listeners, c.listeners)
			c.mu.Unlock()

			for _, fn := range listeners {
				fn(in.Method, in.Params)
			}
		}
	}
}

// Call sends a CDP command and waits for the typed response.
func (c *CDPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal cdp params: %w", err)
		}
		rawParams = data
	}

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if len(rawParams) > 0 {
		req["params"] = rawParams
	}

	rawReq, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal cdp request: %w", err)
	}

	ch := make(chan cdpResult, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.ws.WriteTextMessage(rawReq); err != nil {
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
		return nil, errors.New("cdp connection closed")
	case res := <-ch:
		return res.result, res.err
	}
}

// OnEvent registers a listener callback for CDP notifications.
func (c *CDPClient) OnEvent(fn func(method string, params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

// Close closes the underlying WebSocket connection.
func (c *CDPClient) Close() error {
	return c.ws.Close()
}
