package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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

// SetAuthToken configures the private TLS identity key required for daemon access.
func (s *Server) SetAuthToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authToken = token
}

// AuthToken returns the configured private TLS identity key.
func (s *Server) AuthToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authToken
}

// ListenAndServe binds to 127.0.0.1 on the specified port (or 0 for ephemeral).
func (s *Server) ListenAndServe(port int) error {
	config, err := protocol.DaemonTLS(s.AuthToken(), true)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := tls.Listen("tcp", addr, config)
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
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	secured, ok := conn.(*tls.Conn)
	if !ok || secured.Handshake() != nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	br := bufio.NewReader(conn)
	peek, err := br.Peek(1)
	if err != nil {
		return
	}
	if strings.ContainsRune("PGHODU", rune(peek[0])) {
		// net/http owns header parsing and its hard header limit.
		httpServer := &http.Server{
			MaxHeaderBytes: 16 << 10, ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost {
					w.Header().Set("Allow", "POST")
					w.WriteHeader(405)
					return
				}
				if !isAuthorizedOrigin(req.Header.Get("Origin")) {
					w.WriteHeader(403)
					return
				}
				if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
					w.WriteHeader(415)
					return
				}
				if req.ContentLength > protocol.MaxCommandBytes {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				req.Body = http.MaxBytesReader(w, req.Body, protocol.MaxCommandBytes)
				body, err := io.ReadAll(req.Body)
				if err != nil {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				var rpc protocol.Request
				if json.Unmarshal(body, &rpc) != nil {
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(s.Dispatch(req.Context(), &rpc))
			}),
		}
		listener := &singleConnListener{conn: &bufferedConn{Conn: conn, reader: br}, done: make(chan struct{})}
		_ = httpServer.Serve(listener)
		return
	}
	for {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
		frame, err := protocol.ReadFrame(br, protocol.MaxCommandBytes)
		if err != nil {
			return
		}
		var req protocol.Request
		if json.Unmarshal(frame, &req) != nil {
			return
		}
		if err := json.NewEncoder(conn).Encode(s.Dispatch(context.Background(), &req)); err != nil {
			return
		}
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type singleConnListener struct {
	conn     net.Conn
	done     chan struct{}
	accepted bool
	once     sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return &closingConn{Conn: l.conn, closeListener: l.Close}, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *singleConnListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type closingConn struct {
	net.Conn
	closeListener func() error
}

func (c *closingConn) Close() error { _ = c.closeListener(); return c.Conn.Close() }

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
		res.DaemonVersion, res.DaemonPID = protocol.Version, os.Getpid()
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
