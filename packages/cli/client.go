package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jeffhuen/tether-browser/packages/protocol"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultDaemonAddr is the standard local tunnel address for the tether daemon.
const DefaultDaemonAddr = "127.0.0.1:9333"

// DaemonUnreachableDiagnostic is the required diagnostic message when the daemon cannot be reached.
const DaemonUnreachableDiagnostic = "Error: Cannot connect to tether daemon on 127.0.0.1:9333. Make sure 'tether daemon' is running locally and your SSH tunnel is active (ssh -R 9333:localhost:9333 user@server)."

// Client manages JSON-RPC 2.0 communication with the tether daemon or broker.
type Client struct {
	network string
	addr    string
	epoch   string
	seq     uint64
	timeout time.Duration
	tokenMu sync.RWMutex
	token   string
}

// NewClient initializes a Client pointing to the given network address.
// If addr is empty, DefaultDaemonAddr is used with TCP.
func NewClient(addr string) *Client {
	if addr == "" {
		if env := os.Getenv("TETHER_DAEMON_ADDR"); env != "" {
			addr = env
		} else {
			addr = DefaultDaemonAddr
		}
	}
	network := "tcp"
	if strings.HasPrefix(addr, "unix:") {
		network = "unix"
		addr = strings.TrimPrefix(addr, "unix:")
	} else if strings.HasPrefix(addr, "/") {
		network = "unix"
	}

	return &Client{
		network: network,
		addr:    addr,
		epoch:   fmt.Sprintf("epoch-%d", time.Now().UnixNano()),
		timeout: 30 * time.Second,
		token:   ResolveClientToken(),
	}
}

// SetToken configures the TLS authentication key; it is never sent in RPC.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	c.token = token
	c.tokenMu.Unlock()
}

// SetTimeout configures the request timeout duration.
func (c *Client) SetTimeout(d time.Duration) {
	c.timeout = d
}

// NextSeq returns the next monotonic sequence number.
func (c *Client) NextSeq() uint64 {
	return atomic.AddUint64(&c.seq, 1)
}

// Epoch returns the client epoch token.
func (c *Client) Epoch() string {
	return c.epoch
}

// isConnectionRefused checks if the error indicates a connection failure.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "no such file or directory")
}

// Call executes a single JSON-RPC method call against the daemon.
func (c *Client) Call(ctx context.Context, method string, params any) (*protocol.Response, error) {
	c.tokenMu.RLock()
	token := c.token
	c.tokenMu.RUnlock()
	d := net.Dialer{
		Timeout: c.timeout,
	}

	if c.network == "unix" {
		if err := protocol.ValidatePrivateSocket(c.addr); err != nil {
			return nil, err
		}
	} else if strings.TrimSpace(token) == "" {
		return nil, errors.New("daemon authentication key is missing")
	}
	conn, err := d.DialContext(ctx, c.network, c.addr)
	if err != nil {
		if c.addr == DefaultDaemonAddr || isConnectionRefused(err) {
			return nil, fmt.Errorf("%s", DaemonUnreachableDiagnostic)
		}
		return nil, fmt.Errorf("connect to %s: %w", c.addr, err)
	}
	defer conn.Close()
	if c.network == "unix" {
		if err := protocol.VerifyPeerCredentials(conn); err != nil {
			return nil, err
		}
	}

	// Set TCP_NODELAY on TCP connection to minimize latency (best-effort;
	// forwarded or proxied sockets may return EINVAL on setsockopt).
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
	}
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	if c.network != "unix" {
		secured, err := protocol.AuthenticateDaemon(ctx, conn, token)
		if err != nil {
			return nil, err
		}
		conn = secured
	}

	seq := c.NextSeq()
	reqID := fmt.Sprintf("req-%d", seq)
	req, err := protocol.NewRequest(reqID, method, params, seq, c.epoch)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if len(reqData)+1 > protocol.MaxCommandBytes {
		return nil, errors.New("command exceeds 1MiB size limit")
	}
	reqData = append(reqData, '\n')

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else if c.timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(c.timeout))
	}

	if _, err := conn.Write(reqData); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := protocol.ReadResponse(conn, req)
	if err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp, nil
}
