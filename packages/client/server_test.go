package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/jeffhuen/tether-browser/packages/protocol"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockDriver struct {
	mu           sync.Mutex
	calls        []string
	lastOpen     protocol.OpenParams
	lastClick    protocol.ClickParams
	lastFill     protocol.FillParams
	lastType     protocol.TypeParams
	lastPress    protocol.PressParams
	lastHover    protocol.HoverParams
	lastFocus    protocol.FocusParams
	lastEval     protocol.EvalParams
	lastWait     protocol.WaitParams
	lastShot     protocol.ScreenshotParams
	lastClose    protocol.CloseParams
	snapshotResp *protocol.SnapshotResult
	evalResp     any
	errToReturn  error
}

func (m *mockDriver) OpenTab(ctx context.Context, p protocol.OpenParams) (*protocol.OpenResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "OpenTab")
	m.lastOpen = p
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return &protocol.OpenResult{TargetID: "t-1", URL: p.URL, Title: "Test Page"}, nil
}

func (m *mockDriver) CloseTab(ctx context.Context, p protocol.CloseParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "CloseTab")
	m.lastClose = p
	return m.errToReturn
}

func (m *mockDriver) Snapshot(ctx context.Context, p protocol.SnapshotParams) (*protocol.SnapshotResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Snapshot")
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	if m.snapshotResp != nil {
		return m.snapshotResp, nil
	}
	return &protocol.SnapshotResult{
		Generation: 1,
		RootHash:   "hash-123",
		Nodes: []*protocol.AXNode{
			{Ref: "@e1", Role: "button", Name: "Click Me"},
		},
	}, nil
}

func (m *mockDriver) Click(ctx context.Context, p protocol.ClickParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Click")
	m.lastClick = p
	return m.errToReturn
}

func (m *mockDriver) DblClick(ctx context.Context, p protocol.ClickParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "DblClick")
	m.lastClick = p
	return m.errToReturn
}

func (m *mockDriver) Fill(ctx context.Context, p protocol.FillParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Fill")
	m.lastFill = p
	return m.errToReturn
}

func (m *mockDriver) Type(ctx context.Context, p protocol.TypeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Type")
	m.lastType = p
	return m.errToReturn
}

func (m *mockDriver) Press(ctx context.Context, p protocol.PressParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Press")
	m.lastPress = p
	return m.errToReturn
}

func (m *mockDriver) Hover(ctx context.Context, p protocol.HoverParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Hover")
	m.lastHover = p
	return m.errToReturn
}

func (m *mockDriver) Focus(ctx context.Context, p protocol.FocusParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Focus")
	m.lastFocus = p
	return m.errToReturn
}

func (m *mockDriver) Eval(ctx context.Context, p protocol.EvalParams) (*protocol.EvalResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Eval")
	m.lastEval = p
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return &protocol.EvalResult{Value: m.evalResp}, nil
}

func (m *mockDriver) Wait(ctx context.Context, p protocol.WaitParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Wait")
	m.lastWait = p
	return m.errToReturn
}

func (m *mockDriver) Screenshot(ctx context.Context, p protocol.ScreenshotParams) (*protocol.ScreenshotResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "Screenshot")
	m.lastShot = p
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return &protocol.ScreenshotResult{Base64: "dGVzdA==", Format: "png"}, nil
}

func (m *mockDriver) Status(ctx context.Context, p protocol.StatusParams) (*protocol.StatusResult, error) {
	return &protocol.StatusResult{
		Connected:   true,
		TargetCount: 1,
		Version:     "1.0.0",
	}, nil
}
func (m *mockDriver) ListTabs(ctx context.Context) (*protocol.TabListResult, error) {
	return &protocol.TabListResult{
		Tabs: []protocol.TabInfo{
			{ID: "t-1", Title: "Test Page", URL: "https://example.com", Active: true},
		},
		ActiveID: "t-1",
	}, nil
}

func (m *mockDriver) SwitchTab(ctx context.Context, p protocol.TabSwitchParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "SwitchTab")
	return m.errToReturn
}

func (m *mockDriver) StartReview(ctx context.Context, p protocol.ReviewParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "StartReview")
	return m.errToReturn
}

func (m *mockDriver) GetReviewNotes(ctx context.Context, p protocol.ReviewParams) ([]*protocol.ReviewNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "GetReviewNotes")
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return []*protocol.ReviewNote{
		{
			ID:      "note-1",
			Index:   1,
			Intent:  "design_fix",
			Comment: "Fix button padding",
			Payload: &protocol.ReviewPayload{
				Target: protocol.TargetInfo{
					TagName:  "button",
					Selector: "button.submit",
				},
			},
		},
	}, nil
}

func (m *mockDriver) ClearReview(ctx context.Context, p protocol.ReviewParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "ClearReview")
	return m.errToReturn
}

func TestServerDispatchMethods(t *testing.T) {
	driver := &mockDriver{evalResp: "Hello"}
	server := NewServer(driver)
	ctx := context.Background()

	// 1. Open
	openReq, _ := protocol.NewRequest("req-1", protocol.MethodOpen, protocol.OpenParams{URL: "http://localhost:3000"}, 1, "ep-1")
	openResp := server.Dispatch(ctx, openReq)
	if openResp.Error != nil {
		t.Fatalf("unexpected error: %v", openResp.Error)
	}
	var openRes protocol.OpenResult
	_ = openResp.UnmarshalResult(&openRes)
	if openRes.TargetID != "t-1" {
		t.Errorf("expected targetId t-1, got %s", openRes.TargetID)
	}

	// 2. Click with button and clickCount preserved
	clickReq, _ := protocol.NewRequest("req-2", protocol.MethodClick, protocol.ClickParams{Selector: "@e2", Button: "right", ClickCount: 2}, 2, "ep-1")
	clickResp := server.Dispatch(ctx, clickReq)
	if clickResp.Error != nil {
		t.Fatalf("unexpected click error: %v", clickResp.Error)
	}
	if driver.lastClick.Button != "right" || driver.lastClick.ClickCount != 2 {
		t.Errorf("expected button=right, clickCount=2, got button=%s, clickCount=%d", driver.lastClick.Button, driver.lastClick.ClickCount)
	}

	// 3. Wait with duration and state preserved
	waitReq, _ := protocol.NewRequest("req-3", protocol.MethodWait, protocol.WaitParams{DurationMs: 500, State: "attached"}, 3, "ep-1")
	waitResp := server.Dispatch(ctx, waitReq)
	if waitResp.Error != nil {
		t.Fatalf("unexpected wait error: %v", waitResp.Error)
	}
	if driver.lastWait.DurationMs != 500 || driver.lastWait.State != "attached" {
		t.Errorf("expected duration 500, state attached, got %d, %s", driver.lastWait.DurationMs, driver.lastWait.State)
	}

	// 4. Review Start
	revStartReq, _ := protocol.NewRequest("req-4", protocol.MethodReviewStart, protocol.ReviewParams{}, 4, "ep-1")
	revStartResp := server.Dispatch(ctx, revStartReq)
	if revStartResp.Error != nil {
		t.Fatalf("unexpected review start error: %v", revStartResp.Error)
	}

	// 5. Review Send
	revSendReq, _ := protocol.NewRequest("req-5", protocol.MethodReviewSend, protocol.ReviewParams{}, 5, "ep-1")
	revSendResp := server.Dispatch(ctx, revSendReq)
	if revSendResp.Error != nil {
		t.Fatalf("unexpected review send error: %v", revSendResp.Error)
	}
	var revSendRes protocol.ReviewSendResult
	_ = revSendResp.UnmarshalResult(&revSendRes)
	if !strings.Contains(revSendRes.Markdown, "Fix button padding") {
		t.Errorf("expected markdown to contain feedback comment, got: %s", revSendRes.Markdown)
	}
}

func TestServerHTTPOriginSecurity(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer(driver)

	reqObj, _ := protocol.NewRequest("req-1", protocol.MethodStatus, nil, 1, "")
	body, _ := json.Marshal(reqObj)

	// 1. Request with evil Origin MUST be rejected with 403 Forbidden
	httpReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	httpReq.Header.Set("Origin", "http://malicious-site.com")
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, httpReq)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for external Origin, got %d", w.Code)
	}

	// 2. Request without Content-Type: application/json MUST be rejected with 415
	httpReq2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	httpReq2.Header.Set("Content-Type", "text/plain")
	w2 := httptest.NewRecorder()

	server.ServeHTTP(w2, httpReq2)
	if w2.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415 Unsupported Media Type for text/plain, got %d", w2.Code)
	}
}

func TestServerTokenAuthentication(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer(driver)
	server.SetAuthToken("test-bearer-secret")

	reqObj, _ := protocol.NewRequest("req-auth", protocol.MethodStatus, nil, 1, "")
	body, _ := json.Marshal(reqObj)

	// 1. Unauthenticated HTTP request -> 401 Unauthorized
	httpReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httpReq)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got: %d", w.Code)
	}

	// 2. HTTP request with wrong bearer token -> 401 Unauthorized
	httpReqWrong := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	httpReqWrong.Header.Set("Content-Type", "application/json")
	httpReqWrong.Header.Set("Authorization", "Bearer wrong-token")
	wWrong := httptest.NewRecorder()
	server.ServeHTTP(wWrong, httpReqWrong)
	if wWrong.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized with wrong token, got: %d", wWrong.Code)
	}

	// 3. HTTP request with correct bearer token -> 200 OK
	httpReqAuth := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	httpReqAuth.Header.Set("Content-Type", "application/json")
	httpReqAuth.Header.Set("Authorization", "Bearer test-bearer-secret")
	wAuth := httptest.NewRecorder()
	server.ServeHTTP(wAuth, httpReqAuth)
	if wAuth.Code != http.StatusOK {
		t.Errorf("expected 200 OK with valid bearer token, got: %d", wAuth.Code)
	}
}

func TestServerZstdBinaryFraming(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer(driver)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server.listener = ln
	server.port.Store(int32(ln.Addr().(*net.TCPAddr).Port))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConn(conn)
		}
	}()
	defer server.Close()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()

	// Helper to send request and read framed response
	sendAndReceive := func(id string, text string) *protocol.Response {
		reqObj, _ := protocol.NewRequest(id, protocol.MethodFill, protocol.FillParams{Selector: "@e1", Text: text}, 1, "epoch-1")
		reqBytes, _ := json.Marshal(reqObj)
		compressed, err := protocol.CompressPayload(reqBytes)
		if err != nil {
			t.Fatalf("compress payload: %v", err)
		}
		if _, err := conn.Write(compressed); err != nil {
			t.Fatalf("write compressed request: %v", err)
		}

		// Read response
		br := bufio.NewReader(conn)
		peek, err := br.Peek(1)
		if err != nil {
			t.Fatalf("peek response: %v", err)
		}
		var framedResp []byte
		if peek[0] == protocol.FormatRaw {
			hdr := make([]byte, 5)
			if _, err := io.ReadFull(br, hdr); err != nil {
				t.Fatalf("read raw header: %v", err)
			}
			rawLen := binary.BigEndian.Uint32(hdr[1:5])
			payload := make([]byte, rawLen)
			if _, err := io.ReadFull(br, payload); err != nil {
				t.Fatalf("read raw payload: %v", err)
			}
			framedResp = append(hdr, payload...)
		} else if peek[0] == protocol.FormatZstd {
			hdr := make([]byte, 9)
			if _, err := io.ReadFull(br, hdr); err != nil {
				t.Fatalf("read zstd header: %v", err)
			}
			cLen := binary.BigEndian.Uint32(hdr[1:5])
			payload := make([]byte, cLen)
			if _, err := io.ReadFull(br, payload); err != nil {
				t.Fatalf("read zstd payload: %v", err)
			}
			framedResp = append(hdr, payload...)
		}

		decompressed, err := protocol.DecompressPayload(framedResp)
		if err != nil {
			t.Fatalf("decompress response: %v", err)
		}
		var resp protocol.Response
		if err := json.Unmarshal(decompressed, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return &resp
	}

	// Request 1 on persistent connection
	largeText1 := strings.Repeat("A", 1200)
	resp1 := sendAndReceive("req-large-1", largeText1)
	if protocol.IDString(resp1.ID) != "req-large-1" {
		t.Errorf("expected ID req-large-1, got %s", protocol.IDString(resp1.ID))
	}

	// Request 2 on the SAME persistent connection (proves no stream desync!)
	largeText2 := strings.Repeat("B", 1500)
	resp2 := sendAndReceive("req-large-2", largeText2)
	if protocol.IDString(resp2.ID) != "req-large-2" {
		t.Errorf("expected ID req-large-2, got %s", protocol.IDString(resp2.ID))
	}
}

func TestServerZstdBombRejection(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer(driver)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server.listener = ln
	server.port.Store(int32(ln.Addr().(*net.TCPAddr).Port))

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConn(conn)
		}
	}()
	defer server.Close()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", server.Port()))
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()

	// Send FormatZstd 9-byte header with advertised uncompressed length exceeding MaxFramePayload (33MB)
	bombHeader := make([]byte, 9)
	bombHeader[0] = protocol.FormatZstd
	binary.BigEndian.PutUint32(bombHeader[1:5], 100)
	binary.BigEndian.PutUint32(bombHeader[5:9], protocol.MaxFramePayload+1)
	if _, err := conn.Write(bombHeader); err != nil {
		t.Fatalf("write bomb header: %v", err)
	}

	buf := make([]byte, 10)
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := conn.Read(buf)
	if n > 0 {
		t.Errorf("expected connection close, got %d bytes: %v", n, buf[:n])
	}
	if err == nil {
		t.Errorf("expected EOF or read error on bomb rejection, got nil")
	}
}
