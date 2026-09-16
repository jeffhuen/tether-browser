package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestNativeMessageFraming(t *testing.T) {
	testPayload := []byte(`{"id":"req-1","method":"browser.open","params":{"url":"https://example.com"}}`)

	var buf bytes.Buffer
	if err := WriteNativeMessage(&buf, testPayload); err != nil {
		t.Fatalf("WriteNativeMessage failed: %v", err)
	}

	if buf.Len() != 4+len(testPayload) {
		t.Fatalf("expected buffer length %d, got %d", 4+len(testPayload), buf.Len())
	}

	readBack, err := ReadNativeMessage(&buf)
	if err != nil {
		t.Fatalf("ReadNativeMessage failed: %v", err)
	}

	if !bytes.Equal(readBack, testPayload) {
		t.Fatalf("expected payload %s, got %s", testPayload, readBack)
	}
}

func TestExtensionBridgeOverUnixSocket(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "bridge.sock")
	t.Setenv("TETHER_BRIDGE_SOCKET", socketPath)

	hostToExtReader, hostToExtWriter := io.Pipe()
	extToHostReader, extToHostWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start NativeHost server listening on the private Unix socket
	serverErrChan := make(chan error, 1)
	go func() {
		serverErrChan <- RunNativeHostServer(ctx, extToHostReader, hostToExtWriter)
	}()

	// Wait for socket to become ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 2. Simulate Chrome extension processing commands in background
	go func() {
		for {
			reqBytes, err := ReadNativeMessage(hostToExtReader)
			if err != nil {
				return
			}
			var req nativeRequest
			if err := json.Unmarshal(reqBytes, &req); err != nil {
				continue
			}

			// Respond with mock result
			var mockResult []byte
			switch req.Method {
			case protocol.MethodOpen:
				mockResult, _ = json.Marshal(map[string]any{"targetId": "tab-101", "url": "https://example.com"})
			case protocol.MethodSnapshot:
				mockResult, _ = json.Marshal(map[string]any{"targetId": "tab-101", "rootHash": "hash-5"})
			case protocol.MethodClick:
				resp := nativeResponse{
					ID:   req.ID,
					Type: "error",
					Error: &nativeError{
						Code:    -32000,
						Message: "element not found",
					},
				}
				respBytes, _ := json.Marshal(resp)
				_ = WriteNativeMessage(extToHostWriter, respBytes)
				continue
			default:
				mockResult = []byte(`{}`)
			}

			resp := nativeResponse{
				ID:     req.ID,
				Type:   "response",
				Result: mockResult,
			}
			respBytes, _ := json.Marshal(resp)
			_ = WriteNativeMessage(extToHostWriter, respBytes)
		}
	}()

	// 3. Connect via ExtensionDriver and execute BrowserDriver commands
	driver := NewExtensionDriver(socketPath)
	defer driver.Close()

	if !driver.IsAvailable() {
		t.Fatalf("expected driver.IsAvailable() to be true")
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()

	openRes, err := driver.OpenTab(callCtx, protocol.OpenParams{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("driver.OpenTab failed: %v", err)
	}
	if string(openRes.TargetID) != "tab-101" {
		t.Fatalf("expected targetId tab-101, got %s", openRes.TargetID)
	}

	snapRes, err := driver.Snapshot(callCtx, protocol.SnapshotParams{})
	if err != nil {
		t.Fatalf("driver.Snapshot failed: %v", err)
	}
	if snapRes.RootHash != "hash-5" {
		t.Fatalf("expected rootHash hash-5, got %s", snapRes.RootHash)
	}

	err = driver.Click(callCtx, protocol.ClickParams{Selector: "@e999"})
	if err == nil || !strings.Contains(err.Error(), "element not found") {
		t.Fatalf("expected 'element not found' error, got: %v", err)
	}
}
func TestServerWithExtensionDriver(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "bridge.sock")
	t.Setenv("TETHER_BRIDGE_SOCKET", socketPath)

	hostToExtReader, hostToExtWriter := io.Pipe()
	extToHostReader, extToHostWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = RunNativeHostServer(ctx, extToHostReader, hostToExtWriter)
	}()

	// Wait for socket
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Mock extension
	go func() {
		for {
			reqBytes, err := ReadNativeMessage(hostToExtReader)
			if err != nil {
				return
			}
			var req nativeRequest
			_ = json.Unmarshal(reqBytes, &req)
			mockResult, _ := json.Marshal(map[string]any{"targetId": "tab-server-1", "url": "https://server-test.com"})
			resp := nativeResponse{
				ID:     req.ID,
				Type:   "response",
				Result: mockResult,
			}
			respBytes, _ := json.Marshal(resp)
			_ = WriteNativeMessage(extToHostWriter, respBytes)
		}
	}()

	driver := NewExtensionDriver(socketPath)
	defer driver.Close()

	server := NewServer(driver)
	server.SetAuthToken("auth-token-xyz")

	serverErrChan := make(chan error, 1)
	go func() {
		serverErrChan <- server.ListenAndServe(0)
	}()
	// Wait for server to bind
	var serverPort int
	bindDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(bindDeadline) {
		if p := server.Port(); p > 0 {
			serverPort = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if serverPort == 0 {
		t.Fatalf("server failed to bind port")
	}

	// Send JSON-RPC HTTP request to server
	reqObj, err := protocol.NewRequest("req-server", protocol.MethodOpen, protocol.OpenParams{URL: "https://server-test.com"}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	reqBytes, _ := json.Marshal(reqObj)

	httpReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/", serverPort), bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatal(err)
	}
	httpReq.Header.Set("Authorization", "Bearer auth-token-xyz")
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", httpResp.StatusCode)
	}

	var rpcResp protocol.Response
	if err := json.NewDecoder(httpResp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", rpcResp.Error)
	}
	if !strings.Contains(string(rpcResp.Result), "tab-server-1") {
		t.Fatalf("expected result to contain tab-server-1, got: %s", string(rpcResp.Result))
	}
}
