package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestDaemonUnreachableDiagnostic(t *testing.T) {
	deadAddr := "127.0.0.1:65534"
	t.Setenv("TETHER_DAEMON_ADDR", deadAddr)
	client := NewClient(deadAddr)
	client.SetTimeout(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := client.Call(ctx, protocol.MethodStatus, nil)
	if err == nil {
		t.Fatalf("expected error when daemon is unreachable, got nil")
	}

	if !strings.Contains(err.Error(), DaemonUnreachableDiagnostic) {
		t.Errorf("expected error to contain %q, but got:\n%s", DaemonUnreachableDiagnostic, err.Error())
	}

	// 2. CLI Run() entrypoint output check
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status"}, &stdout, &stderr)
	if code == 0 {
		t.Errorf("expected non-zero exit code when daemon unreachable, got 0")
	}
	if !strings.Contains(stderr.String(), DaemonUnreachableDiagnostic) {
		t.Errorf("expected stderr to contain %q, but got:\n%s", DaemonUnreachableDiagnostic, stderr.String())
	}
}

func TestBrokerLifecycleAndTargetInjection(t *testing.T) {
	// Create mock daemon server
	daemonLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock daemon: %v", err)
	}
	defer daemonLn.Close()

	daemonAddr := daemonLn.Addr().String()

	// Track requests received by daemon
	var receivedMethods []string
	var receivedTargetIDs []string

	go func() {
		for {
			conn, err := daemonLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				decoder := json.NewDecoder(c)
				var req protocol.Request
				if err := decoder.Decode(&req); err != nil {
					return
				}

				receivedMethods = append(receivedMethods, req.Method)

				var paramMap map[string]any
				if len(req.Params) > 0 {
					_ = json.Unmarshal(req.Params, &paramMap)
					if tid, ok := paramMap["targetId"].(string); ok {
						receivedTargetIDs = append(receivedTargetIDs, tid)
					}
				}

				// Respond
				var result any
				if req.Method == protocol.MethodOpen {
					result = protocol.OpenResult{
						TargetID: "target-tab-99",
						URL:      "http://example.com",
					}
				} else {
					result = protocol.ActionResult{OK: true}
				}

				resp, _ := protocol.NewResponse(req.ID, result, req.Seq, req.Epoch)
				data, _ := json.Marshal(resp)
				_, _ = c.Write(append(data, '\n'))
			}(conn)
		}
	}()

	// Start broker on temp socket
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test-broker.sock")

	broker := NewBroker(sockPath, daemonAddr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = broker.Run(ctx)
	}()

	// Wait for broker socket to be ready
	ready := false
	for i := 0; i < 20; i++ {
		if IsBrokerAlive(sockPath) {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("broker socket failed to become ready")
	}

	// Connect client to broker
	cliClient := NewClient("unix:" + sockPath)

	// 1. Send Open command
	openResp, err := cliClient.Call(ctx, protocol.MethodOpen, protocol.OpenParams{URL: "http://example.com"})
	if err != nil {
		t.Fatalf("open via broker failed: %v", err)
	}
	var openRes protocol.OpenResult
	_ = openResp.UnmarshalResult(&openRes)
	if openRes.TargetID != "target-tab-99" {
		t.Errorf("expected target-tab-99, got %q", openRes.TargetID)
	}

	// 2. Send Click command without targetId (broker must inject target-tab-99)
	clickResp, err := cliClient.Call(ctx, protocol.MethodClick, protocol.ClickParams{Selector: "@e5"})
	if err != nil {
		t.Fatalf("click via broker failed: %v", err)
	}
	var actRes protocol.ActionResult
	_ = clickResp.UnmarshalResult(&actRes)
	if !actRes.OK {
		t.Errorf("expected click ActionResult OK true")
	}

	// Stop broker
	broker.Close()
	cancel()

	// Verify socket removed or closed
	time.Sleep(50 * time.Millisecond)
	if IsBrokerAlive(sockPath) {
		t.Errorf("expected broker socket to be closed")
	}

	// Verify targetId was injected into Click call
	foundInjected := false
	for _, tid := range receivedTargetIDs {
		if tid == "target-tab-99" {
			foundInjected = true
			break
		}
	}
	if !foundInjected {
		t.Errorf("expected broker to inject target-tab-99 into click call, got targets: %v", receivedTargetIDs)
	}
}

func TestRunJSONResults(t *testing.T) {
	t.Setenv("TETHER_BROKER_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	screenshotPath := filepath.Join(t.TempDir(), "screenshot.png")
	for _, tc := range []struct {
		name   string
		args   []string
		result json.RawMessage
		code   int
	}{
		{"empty tabs", []string{"tabs", "--json"}, json.RawMessage(`{"tabs":[],"activeId":""}`), 0},
		{"eval error", []string{"eval", "throw new Error('bad')", "--json"}, json.RawMessage(`{"value":null,"error":"bad"}`), 1},
		{"screenshot file", []string{"screenshot", screenshotPath, "--json"}, json.RawMessage(`{"base64":"AAECAw==","format":"png","width":1,"height":1}`), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			t.Setenv("TETHER_DAEMON_ADDR", ln.Addr().String())
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				var req protocol.Request
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				resp, _ := protocol.NewResponse(req.ID, tc.result, req.Seq, req.Epoch)
				_ = json.NewEncoder(conn).Encode(resp)
			}()
			var stdout, stderr bytes.Buffer
			code := Run(tc.args, &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit code %d, expected %d; stderr: %s", code, tc.code, &stderr)
			}
			if tc.args[0] == "screenshot" {
				data, err := os.ReadFile(screenshotPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, []byte{0, 1, 2, 3}) {
					t.Fatalf("saved screenshot bytes differ: %v", data)
				}
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, stdout.Bytes()); err != nil {
				t.Fatalf("expected JSON on stdout, got %q: %v", stdout.String(), err)
			}
			if compact.String() != string(tc.result) {
				t.Fatalf("daemon result changed: %s", &compact)
			}
		})
	}
}
