package client

import (
	"bufio"
	"context"
	"crypto/subtle"
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

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// Server receives JSON-RPC commands from the remote CLI and dispatches them to BrowserDriver.
type Server struct {
	driver    BrowserDriver
	listener  net.Listener
	port      atomic.Int32
	authToken string
	mu        sync.Mutex
	closed    bool
}

// NewServer creates a new daemon RPC server bound to a driver.
func NewServer(driver BrowserDriver) *Server {
	return &Server{
		driver: driver,
	}
}

// SetAuthToken configures a bearer token required for daemon access.
func (s *Server) SetAuthToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authToken = token
}

// AuthToken returns the configured bearer token.
func (s *Server) AuthToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authToken
}

func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) isAuthorized(req *protocol.Request, httpReq *http.Request) bool {
	token := s.AuthToken()
	if token == "" {
		return true
	}
	if httpReq != nil {
		auth := httpReq.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") && tokenEqual(strings.TrimPrefix(auth, "Bearer "), token) {
			return true
		}
		if tokenEqual(httpReq.Header.Get("X-Tether-Token"), token) {
			return true
		}
	}
	if req != nil && tokenEqual(req.Token, token) {
		return true
	}
	return false
}

// ListenAndServe binds to 127.0.0.1 on the specified port (or 0 for ephemeral).
func (s *Server) ListenAndServe(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server listen on %s: %w", addr, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	s.listener = ln
	s.port.Store(int32(ln.Addr().(*net.TCPAddr).Port))
	s.mu.Unlock()
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Port() int {
	return int(s.port.Load())
}

// Close gracefully closes the server listener.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	ln := s.listener
	s.mu.Unlock()

	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	setTCPNoDelay(conn, true)
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	br := bufio.NewReader(conn)

	for {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
		peek, err := br.Peek(1)
		if err != nil {
			return
		}
		firstByte := peek[0]

		// HTTP request detection (POST, GET, etc.)
		if firstByte == 'P' || firstByte == 'G' || firstByte == 'H' || firstByte == 'O' || firstByte == 'D' || firstByte == 'U' {
			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}

			if req.Method != http.MethodPost {
				res := "HTTP/1.1 405 Method Not Allowed\r\nAllow: POST\r\nContent-Length: 0\r\n\r\n"
				_, _ = conn.Write([]byte(res))
				return
			}

			origin := req.Header.Get("Origin")
			if origin != "" && !isAuthorizedOrigin(origin) {
				res := "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"
				_, _ = conn.Write([]byte(res))
				return
			}

			ct := req.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				res := "HTTP/1.1 415 Unsupported Media Type\r\nContent-Length: 0\r\n\r\n"
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
			if !s.isAuthorized(&rpcReq, req) {
				errResp := protocol.NewErrorResponse(rpcReq.ID, protocol.CodeAuthRequired, "authentication required: invalid or missing bearer token", nil, rpcReq.Seq, rpcReq.Epoch)
				respBytes, _ := json.Marshal(errResp)
				fmt.Fprintf(conn, "HTTP/1.1 401 Unauthorized\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
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

		// JSON-RPC stream mode: loop continuously without peeking to preserve read-ahead buffer
		dec := json.NewDecoder(br)
		for {
			var req protocol.Request
			if err := dec.Decode(&req); err != nil {
				return
			}
			if !s.isAuthorized(&req, nil) {
				errResp := protocol.NewErrorResponse(req.ID, protocol.CodeAuthRequired, "authentication required: invalid or missing token", nil, req.Seq, req.Epoch)
				respBytes, _ := json.Marshal(errResp)
				respBytes = append(respBytes, '\n')
				_, _ = conn.Write(respBytes)
				continue
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
}

func isAuthorizedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme == "chrome-extension" {
		return true
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// Dispatch routes a protocol.Request to the appropriate driver method.
func (s *Server) Dispatch(ctx context.Context, req *protocol.Request) *protocol.Response {
	if req.JSONRPC != protocol.JSONRPCVersion {
		return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid jsonrpc version", nil, req.Seq, req.Epoch)
	}

	switch req.Method {
	case protocol.MethodOpen:
		return dispatchParams(req, func(p protocol.OpenParams) (any, error) {
			return s.driver.OpenTab(ctx, p)
		})
	case protocol.MethodClose:
		return dispatchParams(req, func(p protocol.CloseParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.CloseTab(ctx, p)
		})
	case protocol.MethodSnapshot:
		return dispatchParams(req, func(p protocol.SnapshotParams) (any, error) {
			return s.driver.Snapshot(ctx, p)
		})
	case protocol.MethodClick:
		return dispatchParams(req, func(p protocol.ClickParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Click(ctx, p)
		})
	case protocol.MethodDblClick:
		return dispatchParams(req, func(p protocol.ClickParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.DblClick(ctx, p)
		})
	case protocol.MethodFill:
		return dispatchParams(req, func(p protocol.FillParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Fill(ctx, p)
		})
	case protocol.MethodType:
		return dispatchParams(req, func(p protocol.TypeParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Type(ctx, p)
		})
	case protocol.MethodPress:
		return dispatchParams(req, func(p protocol.PressParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Press(ctx, p)
		})
	case protocol.MethodHover:
		return dispatchParams(req, func(p protocol.HoverParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Hover(ctx, p)
		})
	case protocol.MethodFocus:
		return dispatchParams(req, func(p protocol.FocusParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Focus(ctx, p)
		})
	case protocol.MethodEval:
		return dispatchParams(req, func(p protocol.EvalParams) (any, error) {
			return s.driver.Eval(ctx, p)
		})
	case protocol.MethodWait:
		return dispatchParams(req, func(p protocol.WaitParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Wait(ctx, p)
		})
	case protocol.MethodScroll:
		return dispatchParams(req, func(p protocol.ScrollParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.Scroll(ctx, p)
		})
	case protocol.MethodScreenshot:
		return dispatchParams(req, func(p protocol.ScreenshotParams) (any, error) {
			return s.driver.Screenshot(ctx, p)
		})
	case protocol.MethodTabSwitch:
		return dispatchParams(req, func(p protocol.TabSwitchParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.SwitchTab(ctx, p)
		})
	case protocol.MethodReviewStart:
		return dispatchParams(req, func(p protocol.ReviewParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.StartReview(ctx, p)
		})
	case protocol.MethodReviewClear:
		return dispatchParams(req, func(p protocol.ReviewParams) (any, error) {
			return protocol.ActionResult{OK: true}, s.driver.ClearReview(ctx, p)
		})
	case protocol.MethodStatus:
		var p protocol.StatusParams
		res, err := s.driver.Status(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp
	case protocol.MethodTabList:
		res, err := s.driver.ListTabs(ctx)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodReviewList, protocol.MethodReviewSend:
		return dispatchParams(req, func(p protocol.ReviewParams) (any, error) {
			notes, err := s.driver.GetReviewNotes(ctx, p)
			if err != nil {
				return nil, err
			}
			var pageURL, viewport string
			if len(notes) > 0 && notes[0] != nil && notes[0].Payload != nil {
				pageURL = notes[0].Payload.Page.SanitizedURL
				viewport = fmt.Sprintf("%dx%d", notes[0].Payload.Page.ViewportWidth, notes[0].Payload.Page.ViewportHeight)
			}
			if req.Method == protocol.MethodReviewSend {
				md := protocol.FormatDesignFeedbackReport(notes, pageURL, viewport)
				return protocol.ReviewSendResult{Markdown: md, Notes: notes, PageURL: pageURL}, nil
			}
			return protocol.ReviewListResult{Notes: notes, PageURL: pageURL, Viewport: viewport}, nil
		})

	default:
		return protocol.NewErrorResponse(req.ID, protocol.CodeMethodNotFound, "method not found: "+req.Method, nil, req.Seq, req.Epoch)
	}
}

func dispatchParams[P any](req *protocol.Request, call func(P) (any, error)) *protocol.Response {
	var params P
	if err := req.UnmarshalParams(&params); err != nil {
		return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
	}
	result, err := call(params)
	if err != nil {
		return mapDriverError(req, err)
	}
	resp, _ := protocol.NewResponse(req.ID, result, req.Seq, req.Epoch)
	return resp
}

func mapDriverError(req *protocol.Request, err error) *protocol.Response {
	if errors.Is(err, protocol.ErrTargetNotFound) {
		return protocol.NewErrorResponse(req.ID, protocol.CodeTargetNotFound, err.Error(), nil, req.Seq, req.Epoch)
	}
	if errors.Is(err, protocol.ErrStaleRef) {
		return protocol.NewErrorResponse(req.ID, protocol.CodeStaleRef, err.Error(), nil, req.Seq, req.Epoch)
	}
	if errors.Is(err, protocol.ErrActionTimeout) {
		return protocol.NewErrorResponse(req.ID, protocol.CodeActionTimeout, err.Error(), nil, req.Seq, req.Epoch)
	}
	if errors.Is(err, protocol.ErrNotActionable) {
		return protocol.NewErrorResponse(req.ID, protocol.CodeNotActionable, err.Error(), nil, req.Seq, req.Epoch)
	}
	return protocol.NewErrorResponse(req.ID, protocol.CodeInternalError, err.Error(), nil, req.Seq, req.Epoch)
}
