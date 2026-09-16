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
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
	"github.com/klauspost/compress/zstd"
)

// Server receives JSON-RPC commands from the remote CLI and dispatches them to BrowserDriver.
type Server struct {
	driver   BrowserDriver
	listener net.Listener
	port     atomic.Int32
	mu       sync.Mutex
	closed   bool
}

// NewServer creates a new daemon RPC server bound to a driver.
func NewServer(driver BrowserDriver) *Server {
	return &Server{
		driver: driver,
	}
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

			rpcResp := s.Dispatch(req.Context(), &rpcReq)
			respBytes, _ := json.Marshal(rpcResp)
			fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(respBytes), string(respBytes))
			if !req.ProtoAtLeast(1, 1) || req.Close {
				return
			}
			continue
		}

		// Binary framing: FormatRaw
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

		// Binary framing: FormatZstd
		if firstByte == protocol.FormatZstd {
			header := make([]byte, 5)
			if _, err := io.ReadFull(br, header); err != nil {
				return
			}
			uncompressedLen := binary.BigEndian.Uint32(header[1:5])
			if uncompressedLen > protocol.MaxFramePayload {
				return
			}
			dec, err := zstd.NewReader(
				io.LimitReader(br, int64(protocol.MaxFramePayload)),
				zstd.WithDecoderMaxMemory(uint64(protocol.MaxFramePayload)),
				zstd.WithDecodeAllCapLimit(true),
			)
			if err != nil {
				return
			}
			decompressed := make([]byte, uncompressedLen)
			if _, err := io.ReadFull(dec, decompressed); err != nil {
				dec.Close()
				return
			}
			dec.Close()

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

		// JSON-RPC stream mode: loop continuously without peeking to preserve read-ahead buffer
		dec := json.NewDecoder(br)
		for {
			_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
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

// ServeHTTP implements http.Handler for standard HTTP servers and testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	origin := r.Header.Get("Origin")
	if origin != "" && !isAuthorizedOrigin(origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
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
		res, err := s.driver.OpenTab(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodClose:
		var p protocol.CloseParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.CloseTab(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodSnapshot:
		var p protocol.SnapshotParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		res, err := s.driver.Snapshot(ctx, p)
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
		if err := s.driver.Click(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodDblClick:
		var p protocol.ClickParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.DblClick(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodFill:
		var p protocol.FillParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Fill(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodType:
		var p protocol.TypeParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Type(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodPress:
		var p protocol.PressParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Press(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodHover:
		var p protocol.HoverParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Hover(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodFocus:
		var p protocol.FocusParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Focus(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodEval:
		var p protocol.EvalParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		res, err := s.driver.Eval(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodWait:
		var p protocol.WaitParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.Wait(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodScreenshot:
		var p protocol.ScreenshotParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		res, err := s.driver.Screenshot(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodStatus:
		var p protocol.StatusParams
		res, err := s.driver.Status(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, res, req.Seq, req.Epoch)
		return resp

	case protocol.MethodReviewStart:
		var p protocol.ReviewParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.StartReview(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodReviewList:
		var p protocol.ReviewParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		notes, err := s.driver.GetReviewNotes(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		var pageURL, viewport string
		if len(notes) > 0 && notes[0] != nil && notes[0].Payload != nil {
			pageURL = notes[0].Payload.Page.SanitizedURL
			viewport = fmt.Sprintf("%dx%d", notes[0].Payload.Page.ViewportWidth, notes[0].Payload.Page.ViewportHeight)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ReviewListResult{Notes: notes, PageURL: pageURL, Viewport: viewport}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodReviewClear:
		var p protocol.ReviewParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		if err := s.driver.ClearReview(ctx, p); err != nil {
			return mapDriverError(req, err)
		}
		resp, _ := protocol.NewResponse(req.ID, protocol.ActionResult{OK: true}, req.Seq, req.Epoch)
		return resp

	case protocol.MethodReviewSend:
		var p protocol.ReviewParams
		if err := req.UnmarshalParams(&p); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.CodeInvalidParams, err.Error(), nil, req.Seq, req.Epoch)
		}
		notes, err := s.driver.GetReviewNotes(ctx, p)
		if err != nil {
			return mapDriverError(req, err)
		}
		var pageURL, viewport string
		if len(notes) > 0 && notes[0] != nil && notes[0].Payload != nil {
			pageURL = notes[0].Payload.Page.SanitizedURL
			viewport = fmt.Sprintf("%dx%d", notes[0].Payload.Page.ViewportWidth, notes[0].Payload.Page.ViewportHeight)
		}
		md := protocol.FormatDesignFeedbackReport(notes, pageURL, viewport)
		resp, _ := protocol.NewResponse(req.ID, protocol.ReviewSendResult{Markdown: md, Notes: notes, PageURL: pageURL}, req.Seq, req.Epoch)
		return resp

	default:
		return protocol.NewErrorResponse(req.ID, protocol.CodeMethodNotFound, "method not found: "+req.Method, nil, req.Seq, req.Epoch)
	}
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
