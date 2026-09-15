package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestDaemonUnreachableDiagnostic(t *testing.T) {
	// 1. Direct client call to unreachable default daemon
	client := NewClient(DefaultDaemonAddr)
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

func TestClientRPCSuccessAndProtocolFields(t *testing.T) {
	// Mock TCP server listening on ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock tcp listener: %v", err)
	}
	defer ln.Close()

	serverAddr := ln.Addr().String()

	// Handle one request in background
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		decoder := json.NewDecoder(conn)
		var req protocol.Request
		if err := decoder.Decode(&req); err != nil {
			return
		}

		// Verify protocol fields
		if req.JSONRPC != protocol.JSONRPCVersion {
			t.Errorf("expected jsonrpc %s, got %s", protocol.JSONRPCVersion, req.JSONRPC)
		}
		if req.Seq != 1 {
			t.Errorf("expected seq 1, got %d", req.Seq)
		}
		if req.Epoch == "" {
			t.Errorf("expected non-empty epoch")
		}
		if req.Method != protocol.MethodStatus {
			t.Errorf("expected method %s, got %s", protocol.MethodStatus, req.Method)
		}

		// Send mock response
		statusRes := protocol.StatusResult{
			Connected:   true,
			Version:     "0.1.0",
			TargetCount: 1,
		}
		resp, _ := protocol.NewResponse(req.ID, statusRes, req.Seq, req.Epoch)
		data, _ := json.Marshal(resp)
		_, _ = conn.Write(append(data, '\n'))
	}()

	client := NewClient(serverAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.Call(ctx, protocol.MethodStatus, nil)
	if err != nil {
		t.Fatalf("expected successful call, got: %v", err)
	}

	var status protocol.StatusResult
	if err := resp.UnmarshalResult(&status); err != nil {
		t.Fatalf("unmarshal status result: %v", err)
	}
	if !status.Connected || status.Version != "0.1.0" {
		t.Errorf("unexpected status result: %+v", status)
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
