package tests

import (
	"bytes"
	"context"
	"io"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/client"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

type e2eMockDriver struct {
	mu           sync.Mutex
	openCount    int
	lastSelector string
	lastText     string
	reviewNotes  []*protocol.ReviewNote
	closedTabs   []protocol.TargetID
}

func (m *e2eMockDriver) OpenTab(ctx context.Context, p protocol.OpenParams) (*protocol.OpenResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCount++
	targetID := protocol.TargetID(fmt.Sprintf("tab-%d", m.openCount))
	return &protocol.OpenResult{
		TargetID: targetID,
		URL:      p.URL,
		Title:    "E2E Test Page",
	}, nil
}

func (m *e2eMockDriver) CloseTab(ctx context.Context, p protocol.CloseParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedTabs = append(m.closedTabs, p.TargetID)
	return nil
}

func (m *e2eMockDriver) Snapshot(ctx context.Context, p protocol.SnapshotParams) (*protocol.SnapshotResult, error) {
	return &protocol.SnapshotResult{
		Generation: 1,
		RootHash:   "hash-root-1",
		Nodes: []*protocol.AXNode{
			{
				Ref:           "@e1",
				Role:          "button",
				Name:          "Checkout",
				IsInteractive: true,
			},
			{
				Ref:           "@e2",
				Role:          "textbox",
				Name:          "Email",
				Value:         "user@example.com",
				IsInteractive: true,
			},
		},
		RefTable: map[string]int64{
			"@e1": 101,
			"@e2": 102,
		},
		TargetURL: "http://example.test/checkout",
		Title:     "Checkout",
	}, nil
}

func (m *e2eMockDriver) Click(ctx context.Context, p protocol.ClickParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSelector = p.Selector
	return nil
}

func (m *e2eMockDriver) DblClick(ctx context.Context, p protocol.ClickParams) error {
	return m.Click(ctx, p)
}

func (m *e2eMockDriver) Fill(ctx context.Context, p protocol.FillParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSelector = p.Selector
	m.lastText = p.Text
	return nil
}

func (m *e2eMockDriver) Type(ctx context.Context, p protocol.TypeParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSelector = p.Selector
	m.lastText = p.Text
	return nil
}

func (m *e2eMockDriver) Press(ctx context.Context, p protocol.PressParams) error {
	return nil
}

func (m *e2eMockDriver) Hover(ctx context.Context, p protocol.HoverParams) error {
	return nil
}

func (m *e2eMockDriver) Focus(ctx context.Context, p protocol.FocusParams) error {
	return nil
}

func (m *e2eMockDriver) Eval(ctx context.Context, p protocol.EvalParams) (*protocol.EvalResult, error) {
	if strings.Contains(p.Expression, "error") {
		return &protocol.EvalResult{Error: "simulated js evaluation error"}, nil
	}
	return &protocol.EvalResult{Value: "e2e-evaluated-42"}, nil
}

func (m *e2eMockDriver) Wait(ctx context.Context, p protocol.WaitParams) error {
	return nil
}

func (m *e2eMockDriver) Screenshot(ctx context.Context, p protocol.ScreenshotParams) (*protocol.ScreenshotResult, error) {
	return &protocol.ScreenshotResult{
		Base64: "dGVzdC1zY3JlZW5zaG90", // "test-screenshot"
		Format: "png",
		Width:  1280,
		Height: 800,
	}, nil
}

func (m *e2eMockDriver) Status(ctx context.Context, p protocol.StatusParams) (*protocol.StatusResult, error) {
	return &protocol.StatusResult{
		Connected:      true,
		ActiveTargetID: "tab-1",
		TargetCount:    1,
		Version:        "1.0.0",
		Mode:           "managed",
		DaemonUptimeS:  42,
	}, nil
}

func (m *e2eMockDriver) StartReview(ctx context.Context, p protocol.ReviewParams) error {
	return nil
}

func (m *e2eMockDriver) GetReviewNotes(ctx context.Context, p protocol.ReviewParams) ([]*protocol.ReviewNote, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.reviewNotes) > 0 {
		return m.reviewNotes, nil
	}
	return []*protocol.ReviewNote{
		{
			ID:        "note-1",
			Index:     1,
			Intent:    "design_fix",
			Comment:   "Button padding too small on mobile.",
			CreatedAt: time.Now(),
			Payload: &protocol.ReviewPayload{
				Page: protocol.PageInfo{
					SanitizedURL:   "http://localhost:3000/checkout",
					Title:          "Checkout",
					ViewportWidth:  1440,
					ViewportHeight: 900,
				},
				Target: protocol.TargetInfo{
					TagName:        "button",
					Role:           "button",
					AccessibleName: "Checkout",
					Selector:       "button.primary-btn",
					ElementPath:    "main > form > button.primary-btn",
					TextSnippet:    "Checkout",
					CSSClasses:     "btn primary-btn",
					RectViewport:   protocol.Rect{X: 100, Y: 200, Width: 180, Height: 48},
					ComputedStyles: map[string]string{
						"display":          "flex",
						"background-color": "rgb(37, 99, 235)",
					},
					Framework: protocol.FrameworkInfo{
						Name:           "Elixir Phoenix LiveView",
						Component:      "OrderLive",
						SourceLocation: "lib/web/live/order_live.html.heex:42",
						Provenance:     "exact",
					},
				},
			},
		},
	}, nil
}

func (m *e2eMockDriver) ClearReview(ctx context.Context, p protocol.ReviewParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reviewNotes = nil
	return nil
}

func TestEndToEndCLIAutomationCycle(t *testing.T) {
	driver := &e2eMockDriver{}
	server := client.NewServer(driver)

	go func() {
		_ = server.ListenAndServe(0)
	}()
	defer server.Close()

	for i := 0; i < 50; i++ {
		if server.Port() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	daemonAddr := fmt.Sprintf("127.0.0.1:%d", server.Port())
	t.Setenv("TETHER_DAEMON_ADDR", daemonAddr)

	runCLI := func(args ...string) (string, string, int) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(args, &stdout, &stderr)
		return stdout.String(), stderr.String(), code
	}

	// 1. Status
	out, errOut, code := runCLI("status")
	if code != 0 || !strings.Contains(out, "connected") {
		t.Fatalf("status failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 2. Open URL
	out, errOut, code = runCLI("open", "http://example.test/checkout")
	if code != 0 || !strings.Contains(out, "tab-1") {
		t.Fatalf("open failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 3. Snapshot
	out, errOut, code = runCLI("snapshot", "-i")
	if code != 0 || !strings.Contains(out, "[@e1] button \"Checkout\"") {
		t.Fatalf("snapshot failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 4. Click
	out, errOut, code = runCLI("click", "@e1")
	if code != 0 {
		t.Fatalf("click failed (code %d, stderr: %s): %s", code, errOut, out)
	}
	if driver.lastSelector != "@e1" {
		t.Errorf("expected click on @e1, got: %s", driver.lastSelector)
	}

	// 5. Fill with text
	out, errOut, code = runCLI("fill", "@e2", "hello world")
	if code != 0 {
		t.Fatalf("fill failed (code %d, stderr: %s): %s", code, errOut, out)
	}
	if driver.lastSelector != "@e2" || driver.lastText != "hello world" {
		t.Errorf("expected fill on @e2 with 'hello world', got %s %s", driver.lastSelector, driver.lastText)
	}

	// 6. Eval
	out, errOut, code = runCLI("eval", "1+1")
	if code != 0 || !strings.Contains(out, "e2e-evaluated-42") {
		t.Fatalf("eval failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 7. Eval error returns non-zero code
	out, errOut, code = runCLI("eval", "throw error")
	if code == 0 {
		t.Fatalf("expected non-zero code for eval error, got %d (out: %s, errOut: %s)", code, out, errOut)
	}

	// 8. Review Start
	out, errOut, code = runCLI("review", "start")
	if code != 0 || !strings.Contains(out, "Tether Review activated") {
		t.Fatalf("review start failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 9. Review List
	out, errOut, code = runCLI("review", "list")
	if code != 0 || !strings.Contains(out, "Button padding too small") {
		t.Fatalf("review list failed (code %d, stderr: %s): %s", code, errOut, out)
	}

	// 10. Review Send (generates Markdown report)
	out, errOut, code = runCLI("review", "send")
	if code != 0 || !strings.Contains(out, "## Design Feedback: http://localhost:3000/checkout") {
		t.Fatalf("review send failed (code %d, stderr: %s): %s", code, errOut, out)
	}
	if !strings.Contains(out, "Elixir Phoenix LiveView") || !strings.Contains(out, "order_live.html.heex:42") {
		t.Errorf("expected review report to include framework and source, got:\n%s", out)
	}

	// 11. Screenshot
	tmpDir := t.TempDir()
	shotPath := filepath.Join(tmpDir, "screen.png")
	out, errOut, code = runCLI("screenshot", shotPath)
	if code != 0 {
		t.Fatalf("screenshot failed (code %d, stderr: %s): %s", code, errOut, out)
	}
	if info, err := os.Stat(shotPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected screenshot file to be written, got: %v", err)
	}

	// 12. Close
	out, errOut, code = runCLI("close")
	if code != 0 {
		t.Fatalf("close failed (code %d, stderr: %s): %s", code, errOut, out)
	}
}

func TestModeAForwardProxyStreaming(t *testing.T) {
	// Remote mock development server with chunked responses
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "chunk-%d\n", i)
			if ok {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer remoteServer.Close()

	remoteHostPort := strings.TrimPrefix(remoteServer.URL, "http://")

	proxy := client.NewProxy()
	go func() {
		_ = proxy.ListenAndServe(0)
	}()
	defer proxy.Close()
	time.Sleep(50 * time.Millisecond)

	// Enroll localhost:3000 -> remoteServer
	proxy.Enroll("localhost:3000", remoteHostPort)

	// Make request through proxy
	req, err := http.NewRequest(http.MethodGet, "http://localhost:3000/stream", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", proxy.Port())
	proxyClient := &http.Client{
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				return url.Parse(proxyURL)
			},
		},
		Timeout: 5 * time.Second,
	}

	resp, err := proxyClient.Do(req)
	if err != nil {
		t.Fatalf("execute request through proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", resp.StatusCode)
	}

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	bodyStr := buf.String()
	for i := 1; i <= 3; i++ {
		chunk := fmt.Sprintf("chunk-%d", i)
		if !strings.Contains(bodyStr, chunk) {
			t.Errorf("expected body to contain %q, got: %s", chunk, bodyStr)
		}
	}
}
