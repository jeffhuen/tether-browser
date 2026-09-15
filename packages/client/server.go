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
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// DefaultServerAddr is the default local daemon RPC listening address.
const DefaultServerAddr = "127.0.0.1:9333"

// Server handles JSON-RPC 2.0 requests over TCP and HTTP.
type Server struct {
	addr      string
	driver    BrowserDriver
	listener  net.Listener
	mu        sync.Mutex
	closed    atomic.Bool
	startTime time.Time
}

// NewServer creates a new daemon RPC server.
func NewServer(addr string, driver BrowserDriver) *Server {
	if addr == "" {
		addr = DefaultServerAddr
	}
	return &Server{
		addr:      addr,
		driver:    driver,
		startTime: time.Now(),
	}
}

// Start begins listening for RPC requests.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return nil
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("server listen on %s: %w", s.addr, err)
	}
	s.listener = ln

	go s.acceptLoop(ln)
	return nil
}

// Port returns the bound port number.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Addr returns the listener address string.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Close stops the server and closes the listener.
func (s *Server) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.listener != nil {
			return s.listener.Close()
		}
	}
	return nil
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closed.Load() {
				return
			}
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)

	for {
		peek, err := br.Peek(1)
		if err != nil {
			return
		}

		firstByte := peek[0]
		// HTTP request detection (GET, POST, HEAD, OPTIONS)
		if firstByte == 'P' || firstByte == 'G' || firstByte == 'H' || firstByte == 'O' {
			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}

			if req.Method != http.MethodPost {
				res := "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n"
				_, _ = conn.Write([]byte(res))
				return
			}

			body, err := io.ReadAll(req.Body)
			_ = req.Body.Close()
			if err != nil {
				res := "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"
				_, _ = conn.Write([]byte(res))
				return
			}

			var rpcReq protocol.Request
			if err := json.Unmarshal(body, &rpcReq); err != nil {
				errResp := protocol.NewErrorResponse(nil, protocol.CodeParseError, "parse error", nil, 0, "")
				respBytes, _ := json.Marshal(errResp)
				fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
				return
			}

			rpcResp := s.Dispatch(req.Context(), &rpcReq)
			respBytes, _ := json.Marshal(rpcResp)
			fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
			if !req.ProtoAtLeast(1, 1) || req.Close {
				return
			}
			continue
		}

		// FormatRaw binary framing
		if firstByte == protocol.FormatRaw {
			header := make([]byte, 5)
			if _, err := io.ReadFull(br, header); err != nil {
				return
			}
			uncompressedLen := binary.BigEndian.Uint32(header[1:5])
			if uncompressedLen > protocol.MaxFramePayload {
				return
			}
			payload := make([]byte, uncompressedLen)
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			framed := append(header, payload...)
			decompressed, err := protocol.DecompressPayload(framed)
			if err != nil {
				return
			}
			var req protocol.Request
			if err := json.Unmarshal(decompressed, &req); err != nil {
				return
			}
			resp := s.Dispatch(context.Background(), &req)
			respJSON, _ := json.Marshal(resp)
			respFramed, err := protocol.CompressPayload(respJSON)
			if err != nil {
				return
			}
			if _, err := conn.Write(respFramed); err != nil {
				return
			}
			continue
		}

		// JSON-RPC stream / newline-delimited JSON
		dec := json.NewDecoder(br)
		var req protocol.Request
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := s.Dispatch(context.Background(), &req)
		respBytes, err := json.Marshal(resp)
		if err != nil {
			return
		}
		respBytes = append(respBytes, '\n')
		if _, err := conn.Write(respBytes); err != nil {
			return
		}
	}
}

// ServeHTTP implements http.Handler for standard HTTP servers and testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(body, &req); err != nil {
		errResp := protocol.NewErrorResponse(nil, protocol.CodeParseError, "parse error", nil, 0, "")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(errResp)
		return
	}

	resp := s.Dispatch(r.Context(), &req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Dispatch routes a protocol.Request to the appropriate driver method.
func (s *Server) Dispatch(ctx context.Context, req *protocol.Request) *protocol.Response {
	if req.JSONRPC != protocol.JSONRPCVersion {
		return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version", nil, req.Seq, req.Epoch)
	}

	switch req.Method {
	case protocol.MethodOpen:
		var p protocol.OpenParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		targetID, err := s.driver.OpenTab(ctx, p.URL)
		if err != nil {
			return mapDriverError(req, err)
		}
		res := protocol.OpenResult{
			TargetID: targetID,
			URL:      p.URL,
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodClose:
		var p protocol.CloseParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.CloseTab(ctx, p.TargetID); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodSnapshot:
		var p protocol.SnapshotParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		res, err := s.driver.Snapshot(ctx, p.TargetID, p.InteractiveOnly)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodClick:
		var p protocol.ClickParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Click(ctx, p.TargetID, p.Selector); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodFill:
		var p protocol.FillParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Fill(ctx, p.TargetID, p.Selector, p.Text); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodType:
		var p protocol.TypeParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Type(ctx, p.TargetID, p.Selector, p.Text); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodPress:
		var p protocol.PressParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Press(ctx, p.TargetID, p.Key); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodHover:
		var p protocol.HoverParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Hover(ctx, p.TargetID, p.Selector); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodFocus:
		var p protocol.FocusParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Focus(ctx, p.TargetID, p.Selector); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodEval:
		var p protocol.EvalParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		val, err := s.driver.Eval(ctx, p.TargetID, p.Expression)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.EvalResult{Value: val}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodWait:
		var p protocol.WaitParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		timeout := p.TimeoutMs
		if timeout == 0 && p.DurationMs > 0 {
			timeout = p.DurationMs
		}
		if err := s.driver.Wait(ctx, p.TargetID, p.Selector, timeout); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodScreenshot:
		var p protocol.ScreenshotParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		res, err := s.driver.Screenshot(ctx, p.TargetID, p.FullPage)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodStatus:
		uptime := int64(time.Since(s.startTime).Seconds())
		res := protocol.StatusResult{
			Connected:     true,
			Version:       "1.0.0",
			Mode:          "managed",
			DaemonUptimeS: uptime,
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case "browser.review.start", "review.start":
		var p struct {
			TargetID protocol.TargetID `json:"targetId"`
		}
		_ = req.UnmarshalParams(&p)
		if err := s.driver.StartReview(ctx, p.TargetID); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case "browser.review.notes", "review.notes":
		var p struct {
			TargetID protocol.TargetID `json:"targetId"`
		}
		_ = req.UnmarshalParams(&p)
		res, err := s.driver.GetReviewNotes(ctx, p.TargetID)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	default:
		return protocol.NewErrorResponse(req.ID, protocol.CodeMethodNotFound, fmt.Sprintf("method %q not found", req.Method), nil, req.Seq, req.Epoch)
	}
}

func mapDriverError(req *protocol.Request, err error) *protocol.Response {
	var rpcErr *protocol.RPCError
	if errors.As(err, &rpcErr) {
		return protocol.NewErrorResponse(req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data, req.Seq, req.Epoch)
	}
	switch {
	case errors.Is(err, protocol.ErrTargetNotFound):
		return protocol.NewErrorResponse(req.ID, protocol.CodeTargetNotFound, err.Error(), nil, req.Seq, req.Epoch)
	case errors.Is(err, protocol.ErrStaleRef):
		return protocol.NewErrorResponse(req.ID, protocol.CodeStaleRef, err.Error(), nil, req.Seq, req.Epoch)
	case errors.Is(err, protocol.ErrActionTimeout):
		return protocol.NewErrorResponse(req.ID, protocol.CodeActionTimeout, err.Error(), nil, req.Seq, req.Epoch)
	case errors.Is(err, protocol.ErrNotActionable):
		return protocol.NewErrorResponse(req.ID, protocol.CodeNotActionable, err.Error(), nil, req.Seq, req.Epoch)
	case errors.Is(err, protocol.ErrNavigationError):
		return protocol.NewErrorResponse(req.ID, protocol.CodeNavigationError, err.Error(), nil, req.Seq, req.Epoch)
	case errors.Is(err, protocol.ErrAuthRequired):
		return protocol.NewErrorResponse(req.ID, protocol.CodeAuthRequired, err.Error(), nil, req.Seq, req.Epoch)
	default:
		return protocol.NewErrorResponse(req.ID, protocol.CodeInternalError, err.Error(), nil, req.Seq, req.Epoch)
	}
}
