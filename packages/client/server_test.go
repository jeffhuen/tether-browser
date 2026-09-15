package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// mockDriver records calls and returns configured stub responses.
type mockDriver struct {
	openTabFn       func(ctx context.Context, url string) (protocol.TargetID, error)
	closeTabFn      func(ctx context.Context, target protocol.TargetID) error
	snapshotFn      func(ctx context.Context, target protocol.TargetID, interactiveOnly bool) (*protocol.SnapshotResult, error)
	clickFn         func(ctx context.Context, target protocol.TargetID, selector string) error
	fillFn          func(ctx context.Context, target protocol.TargetID, selector, text string) error
	typeFn          func(ctx context.Context, target protocol.TargetID, selector, text string) error
	pressFn         func(ctx context.Context, target protocol.TargetID, key string) error
	hoverFn         func(ctx context.Context, target protocol.TargetID, selector string) error
	focusFn         func(ctx context.Context, target protocol.TargetID, selector string) error
	evalFn          func(ctx context.Context, target protocol.TargetID, script string) (any, error)
	waitFn          func(ctx context.Context, target protocol.TargetID, selector string, timeoutMs int) error
	screenshotFn    func(ctx context.Context, target protocol.TargetID, fullPage bool) (*protocol.ScreenshotResult, error)
	startReviewFn   func(ctx context.Context, target protocol.TargetID) error
	getReviewNotesFn func(ctx context.Context, target protocol.TargetID) (*protocol.ReviewPayload, error)
}

func (m *mockDriver) OpenTab(ctx context.Context, url string) (protocol.TargetID, error) {
	if m.openTabFn != nil {
		return m.openTabFn(ctx, url)
	}
	return protocol.TargetID("tab-1"), nil
}

func (m *mockDriver) CloseTab(ctx context.Context, target protocol.TargetID) error {
	if m.closeTabFn != nil {
		return m.closeTabFn(ctx, target)
	}
	return nil
}

func (m *mockDriver) Snapshot(ctx context.Context, target protocol.TargetID, interactiveOnly bool) (*protocol.SnapshotResult, error) {
	if m.snapshotFn != nil {
		return m.snapshotFn(ctx, target, interactiveOnly)
	}
	return &protocol.SnapshotResult{
		Generation: 1,
		RootHash:   "hash-abc",
		Nodes:      []*protocol.AXNode{{Ref: "@e1", Role: "button", Name: "Click Me"}},
		RefTable:   map[string]int64{"@e1": 1},
		TargetURL:  "http://example.com",
		Title:      "Example",
	}, nil
}

func (m *mockDriver) Click(ctx context.Context, target protocol.TargetID, selector string) error {
	if m.clickFn != nil {
		return m.clickFn(ctx, target, selector)
	}
	return nil
}

func (m *mockDriver) Fill(ctx context.Context, target protocol.TargetID, selector, text string) error {
	if m.fillFn != nil {
		return m.fillFn(ctx, target, selector, text)
	}
	return nil
}

func (m *mockDriver) Type(ctx context.Context, target protocol.TargetID, selector, text string) error {
	if m.typeFn != nil {
		return m.typeFn(ctx, target, selector, text)
	}
	return nil
}

func (m *mockDriver) Press(ctx context.Context, target protocol.TargetID, key string) error {
	if m.pressFn != nil {
		return m.pressFn(ctx, target, key)
	}
	return nil
}

func (m *mockDriver) Hover(ctx context.Context, target protocol.TargetID, selector string) error {
	if m.hoverFn != nil {
		return m.hoverFn(ctx, target, selector)
	}
	return nil
}

func (m *mockDriver) Focus(ctx context.Context, target protocol.TargetID, selector string) error {
	if m.focusFn != nil {
		return m.focusFn(ctx, target, selector)
	}
	return nil
}

func (m *mockDriver) Eval(ctx context.Context, target protocol.TargetID, script string) (any, error) {
	if m.evalFn != nil {
		return m.evalFn(ctx, target, script)
	}
	return "eval-result", nil
}

func (m *mockDriver) Wait(ctx context.Context, target protocol.TargetID, selector string, timeoutMs int) error {
	if m.waitFn != nil {
		return m.waitFn(ctx, target, selector, timeoutMs)
	}
	return nil
}

func (m *mockDriver) Screenshot(ctx context.Context, target protocol.TargetID, fullPage bool) (*protocol.ScreenshotResult, error) {
	if m.screenshotFn != nil {
		return m.screenshotFn(ctx, target, fullPage)
	}
	return &protocol.ScreenshotResult{
		Base64: "base64-bytes",
		Format: "png",
	}, nil
}

func (m *mockDriver) StartReview(ctx context.Context, target protocol.TargetID) error {
	if m.startReviewFn != nil {
		return m.startReviewFn(ctx, target)
	}
	return nil
}

func (m *mockDriver) GetReviewNotes(ctx context.Context, target protocol.TargetID) (*protocol.ReviewPayload, error) {
	if m.getReviewNotesFn != nil {
		return m.getReviewNotesFn(ctx, target)
	}
	return &protocol.ReviewPayload{
		Page: protocol.PageInfo{SanitizedURL: "http://example.com", Title: "Example"},
	}, nil
}

func TestServerDispatchMethods(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer("127.0.0.1:0", driver)
	ctx := context.Background()

	// browser.open
	openReq, _ := protocol.NewRequest("req-1", protocol.MethodOpen, protocol.OpenParams{URL: "http://test.local"}, 1, "ep-1")
	resp := server.Dispatch(ctx, openReq)
	if resp.Error != nil {
		t.Fatalf("open returned error: %v", resp.Error)
	}
	var openRes protocol.OpenResult
	if err := resp.UnmarshalResult(&openRes); err != nil {
		t.Fatalf("unmarshal open result: %v", err)
	}
	if openRes.TargetID != "tab-1" {
		t.Fatalf("expected tab-1, got: %s", openRes.TargetID)
	}

	// browser.snapshot
	snapReq, _ := protocol.NewRequest("req-2", protocol.MethodSnapshot, protocol.SnapshotParams{InteractiveOnly: true}, 2, "ep-1")
	resp = server.Dispatch(ctx, snapReq)
	if resp.Error != nil {
		t.Fatalf("snapshot returned error: %v", resp.Error)
	}
	var snapRes protocol.SnapshotResult
	if err := resp.UnmarshalResult(&snapRes); err != nil {
		t.Fatalf("unmarshal snapshot result: %v", err)
	}
	if len(snapRes.Nodes) != 1 || snapRes.Nodes[0].Ref != "@e1" {
		t.Fatalf("unexpected snapshot nodes: %+v", snapRes.Nodes)
	}

	// browser.click
	clickReq, _ := protocol.NewRequest("req-3", protocol.MethodClick, protocol.ClickParams{Selector: "@e1"}, 3, "ep-1")
	resp = server.Dispatch(ctx, clickReq)
	if resp.Error != nil {
		t.Fatalf("click returned error: %v", resp.Error)
	}

	// browser.fill
	fillReq, _ := protocol.NewRequest("req-4", protocol.MethodFill, protocol.FillParams{Selector: "@e1", Text: "user"}, 4, "ep-1")
	resp = server.Dispatch(ctx, fillReq)
	if resp.Error != nil {
		t.Fatalf("fill returned error: %v", resp.Error)
	}

	// browser.type
	typeReq, _ := protocol.NewRequest("req-5", protocol.MethodType, protocol.TypeParams{Text: "pass"}, 5, "ep-1")
	resp = server.Dispatch(ctx, typeReq)
	if resp.Error != nil {
		t.Fatalf("type returned error: %v", resp.Error)
	}

	// browser.press
	pressReq, _ := protocol.NewRequest("req-6", protocol.MethodPress, protocol.PressParams{Key: "Enter"}, 6, "ep-1")
	resp = server.Dispatch(ctx, pressReq)
	if resp.Error != nil {
		t.Fatalf("press returned error: %v", resp.Error)
	}

	// browser.hover
	hoverReq, _ := protocol.NewRequest("req-7", protocol.MethodHover, protocol.HoverParams{Selector: "@e1"}, 7, "ep-1")
	resp = server.Dispatch(ctx, hoverReq)
	if resp.Error != nil {
		t.Fatalf("hover returned error: %v", resp.Error)
	}

	// browser.focus
	focusReq, _ := protocol.NewRequest("req-8", protocol.MethodFocus, protocol.FocusParams{Selector: "@e1"}, 8, "ep-1")
	resp = server.Dispatch(ctx, focusReq)
	if resp.Error != nil {
		t.Fatalf("focus returned error: %v", resp.Error)
	}

	// browser.eval
	evalReq, _ := protocol.NewRequest("req-9", protocol.MethodEval, protocol.EvalParams{Expression: "1 + 1"}, 9, "ep-1")
	resp = server.Dispatch(ctx, evalReq)
	if resp.Error != nil {
		t.Fatalf("eval returned error: %v", resp.Error)
	}
	var evalRes protocol.EvalResult
	if err := resp.UnmarshalResult(&evalRes); err != nil {
		t.Fatalf("unmarshal eval result: %v", err)
	}
	if evalRes.Value != "eval-result" {
		t.Fatalf("unexpected eval result value: %v", evalRes.Value)
	}

	// browser.wait
	waitReq, _ := protocol.NewRequest("req-10", protocol.MethodWait, protocol.WaitParams{Selector: "#btn"}, 10, "ep-1")
	resp = server.Dispatch(ctx, waitReq)
	if resp.Error != nil {
		t.Fatalf("wait returned error: %v", resp.Error)
	}

	// browser.screenshot
	ssReq, _ := protocol.NewRequest("req-11", protocol.MethodScreenshot, protocol.ScreenshotParams{FullPage: true}, 11, "ep-1")
	resp = server.Dispatch(ctx, ssReq)
	if resp.Error != nil {
		t.Fatalf("screenshot returned error: %v", resp.Error)
	}
	var ssRes protocol.ScreenshotResult
	if err := resp.UnmarshalResult(&ssRes); err != nil {
		t.Fatalf("unmarshal screenshot: %v", err)
	}
	if ssRes.Base64 != "base64-bytes" {
		t.Fatalf("unexpected screenshot base64: %s", ssRes.Base64)
	}

	// browser.close
	closeReq, _ := protocol.NewRequest("req-12", protocol.MethodClose, protocol.CloseParams{TargetID: "tab-1"}, 12, "ep-1")
	resp = server.Dispatch(ctx, closeReq)
	if resp.Error != nil {
		t.Fatalf("close returned error: %v", resp.Error)
	}

	// browser.status
	statusReq, _ := protocol.NewRequest("req-13", protocol.MethodStatus, nil, 13, "ep-1")
	resp = server.Dispatch(ctx, statusReq)
	if resp.Error != nil {
		t.Fatalf("status returned error: %v", resp.Error)
	}
	var statusRes protocol.StatusResult
	if err := resp.UnmarshalResult(&statusRes); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !statusRes.Connected {
		t.Fatalf("expected status connected=true")
	}

	// review.start and review.notes
	revStartReq, _ := protocol.NewRequest("req-14", "review.start", nil, 14, "ep-1")
	resp = server.Dispatch(ctx, revStartReq)
	if resp.Error != nil {
		t.Fatalf("review.start returned error: %v", resp.Error)
	}

	revNotesReq, _ := protocol.NewRequest("req-15", "review.notes", nil, 15, "ep-1")
	resp = server.Dispatch(ctx, revNotesReq)
	if resp.Error != nil {
		t.Fatalf("review.notes returned error: %v", resp.Error)
	}
}

func TestServerErrors(t *testing.T) {
	driver := &mockDriver{
		clickFn: func(ctx context.Context, target protocol.TargetID, selector string) error {
			return protocol.ErrTargetNotFound
		},
		waitFn: func(ctx context.Context, target protocol.TargetID, selector string, timeoutMs int) error {
			return protocol.ErrActionTimeout
		},
	}
	server := NewServer("127.0.0.1:0", driver)
	ctx := context.Background()

	// Invalid JSONRPC version
	badVerReq := &protocol.Request{
		JSONRPC: "1.0",
		ID:      protocol.FormatID("bad-ver"),
		Method:  protocol.MethodOpen,
	}
	resp := server.Dispatch(ctx, badVerReq)
	if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("expected CodeInvalidRequest, got: %v", resp.Error)
	}

	// Unknown method
	unknownReq, _ := protocol.NewRequest("req-unknown", "browser.nonexistent", nil, 1, "ep-1")
	resp = server.Dispatch(ctx, unknownReq)
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodNotFound {
		t.Fatalf("expected CodeMethodNotFound, got: %v", resp.Error)
	}

	// Invalid params
	badParamsReq := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      protocol.FormatID("bad-params"),
		Method:  protocol.MethodOpen,
		Params:  []byte(`"not-an-object"`),
	}
	resp = server.Dispatch(ctx, badParamsReq)
	if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("expected CodeInvalidParams, got: %v", resp.Error)
	}

	// Target not found error code mapping
	clickReq, _ := protocol.NewRequest("req-click-err", protocol.MethodClick, protocol.ClickParams{Selector: "#missing"}, 1, "ep-1")
	resp = server.Dispatch(ctx, clickReq)
	if resp.Error == nil || resp.Error.Code != protocol.CodeTargetNotFound {
		t.Fatalf("expected CodeTargetNotFound, got: %v", resp.Error)
	}

	// Action timeout error code mapping
	waitReq, _ := protocol.NewRequest("req-wait-err", protocol.MethodWait, protocol.WaitParams{Selector: "#timeout"}, 1, "ep-1")
	resp = server.Dispatch(ctx, waitReq)
	if resp.Error == nil || resp.Error.Code != protocol.CodeActionTimeout {
		t.Fatalf("expected CodeActionTimeout, got: %v", resp.Error)
	}
}

func TestServerOverHTTP(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer("127.0.0.1:0", driver)

	reqObj, _ := protocol.NewRequest("http-req", protocol.MethodOpen, protocol.OpenParams{URL: "http://example.com"}, 1, "ep-1")
	reqData, _ := json.Marshal(reqObj)

	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(reqData)))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, httpReq)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %d", res.StatusCode)
	}

	var resp protocol.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %v", resp.Error)
	}
	if protocol.IDString(resp.ID) != "http-req" {
		t.Fatalf("expected id http-req, got %s", protocol.IDString(resp.ID))
	}
}

func TestServerOverTCPStream(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer("127.0.0.1:0", driver)
	if err := server.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer server.Close()

	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	// Send request 1
	req1, _ := protocol.NewRequest("stream-1", protocol.MethodOpen, protocol.OpenParams{URL: "http://example.com"}, 1, "ep-1")
	if err := enc.Encode(req1); err != nil {
		t.Fatalf("encode req 1: %v", err)
	}

	var resp1 protocol.Response
	if err := dec.Decode(&resp1); err != nil {
		t.Fatalf("decode resp 1: %v", err)
	}
	if protocol.IDString(resp1.ID) != "stream-1" {
		t.Fatalf("expected id stream-1, got %s", protocol.IDString(resp1.ID))
	}

	// Send request 2 on the same connection
	req2, _ := protocol.NewRequest("stream-2", protocol.MethodStatus, nil, 2, "ep-1")
	if err := enc.Encode(req2); err != nil {
		t.Fatalf("encode req 2: %v", err)
	}

	var resp2 protocol.Response
	if err := dec.Decode(&resp2); err != nil {
		t.Fatalf("decode resp 2: %v", err)
	}
	if protocol.IDString(resp2.ID) != "stream-2" {
		t.Fatalf("expected id stream-2, got %s", protocol.IDString(resp2.ID))
	}
}

func TestServerOverTCPBinaryFraming(t *testing.T) {
	driver := &mockDriver{}
	server := NewServer("127.0.0.1:0", driver)
	if err := server.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer server.Close()

	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()

	req, _ := protocol.NewRequest("bin-1", protocol.MethodOpen, protocol.OpenParams{URL: "http://example.com"}, 1, "ep-1")
	reqJSON, _ := json.Marshal(req)

	framedReq, err := protocol.CompressPayload(reqJSON)
	if err != nil {
		t.Fatalf("compress req: %v", err)
	}

	if _, err := conn.Write(framedReq); err != nil {
		t.Fatalf("write framed req: %v", err)
	}

	// Read response framed header
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read header: %v", err)
	}
	uncompressedLen := binary.BigEndian.Uint32(header[1:5])
	payload := make([]byte, uncompressedLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}

	decompressed, err := protocol.DecompressPayload(append(header, payload...))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	var resp protocol.Response
	if err := json.Unmarshal(decompressed, &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if protocol.IDString(resp.ID) != "bin-1" {
		t.Fatalf("expected id bin-1, got: %s", protocol.IDString(resp.ID))
	}
}
