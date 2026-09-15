package client

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func startMockCDPServer(t *testing.T) (net.Listener, string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock cdp: %v", err)
	}

	addr := ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockCDPConn(conn, addr)
		}
	}()

	return ln, addr
}

func handleMockCDPConn(conn net.Conn, serverAddr string) {
	defer conn.Close()
	br := bufio.NewReader(conn)

	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	// Handle /json/new
	if strings.HasPrefix(req.URL.Path, "/json/new") {
		resp := targetInfo{
			ID:                   "tab-mock-1",
			Type:                 "page",
			URL:                  "http://example.com",
			Title:                "Mock Page",
			WebSocketDebuggerURL: fmt.Sprintf("ws://%s/devtools/page/tab-mock-1", serverAddr),
		}
		body, _ := json.Marshal(resp)
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), string(body))
		return
	}

	// Handle /json/close
	if strings.HasPrefix(req.URL.Path, "/json/close") {
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 6\r\n\r\nTarget")
		return
	}

	// Handle WebSocket Upgrade
	if strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
		challengeKey := req.Header.Get("Sec-WebSocket-Key")
		const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
		h := sha1.New()
		h.Write([]byte(challengeKey + magicGUID))
		accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)

		// Process incoming CDP RPC requests over WebSocket
		for {
			header := make([]byte, 2)
			if _, err := io.ReadFull(br, header); err != nil {
				return
			}
			rawLen := int(header[1] & 0x7F)
			var payloadLen int
			if rawLen <= 125 {
				payloadLen = rawLen
			} else if rawLen == 126 {
				ext := make([]byte, 2)
				if _, err := io.ReadFull(br, ext); err != nil {
					return
				}
				payloadLen = int(binary.BigEndian.Uint16(ext))
			} else {
				ext := make([]byte, 8)
				if _, err := io.ReadFull(br, ext); err != nil {
					return
				}
				payloadLen = int(binary.BigEndian.Uint64(ext))
			}

			maskKey := make([]byte, 4)
			if _, err := io.ReadFull(br, maskKey); err != nil {
				return
			}
			payload := make([]byte, payloadLen)
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			for i := range payload {
				payload[i] ^= maskKey[i%4]
			}

			var cdpReq struct {
				ID     int64          `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.Unmarshal(payload, &cdpReq)

			// Construct mock response
			var result any
			switch cdpReq.Method {
			case "Runtime.evaluate":
				expr, _ := cdpReq.Params["expression"].(string)
				if strings.Contains(expr, "isInteractive") {
					// Mock snapshot extraction script
					result = map[string]any{
						"result": map[string]any{
							"type": "object",
							"value": map[string]any{
								"root": []map[string]any{
									{
										"ref":           "@e1",
										"backendNodeId": 101,
										"role":          "button",
										"name":          "Submit",
										"isInteractive": true,
									},
								},
								"refMap": map[string]any{
									"@e1": 101,
								},
								"title": "Mock Page",
								"url":   "http://example.com",
							},
						},
					}
				} else if strings.Contains(expr, "getBoundingClientRect") {
					// Mock element resolver for click/hover
					result = map[string]any{
						"result": map[string]any{
							"type": "object",
							"value": map[string]any{
								"ok": true,
								"x":  150.0,
								"y":  250.0,
							},
						},
					}
				} else {
					result = map[string]any{
						"result": map[string]any{
							"type":  "string",
							"value": "evaluated-ok",
						},
					}
				}
			case "Page.captureScreenshot":
				result = map[string]any{
					"data": "mock-screenshot-base64",
				}
			default:
				result = map[string]any{}
			}

			respObj := map[string]any{
				"id":     cdpReq.ID,
				"result": result,
			}
			respBytes, _ := json.Marshal(respObj)
			sendServerWSFrame(conn, respBytes)
		}
	}
}

func sendServerWSFrame(conn net.Conn, payload []byte) {
	length := len(payload)
	var header []byte
	if length <= 125 {
		header = []byte{0x81, byte(length)}
	} else if length <= 65535 {
		header = make([]byte, 4)
		header[0] = 0x81
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
	} else {
		header = make([]byte, 10)
		header[0] = 0x81
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
	}

	_, _ = conn.Write(header)
	_, _ = conn.Write(payload)
}

func TestCDPDriverFullCycle(t *testing.T) {
	serverLn, serverAddr := startMockCDPServer(t)
	defer serverLn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	driver := NewCDPDriver("http://" + serverAddr)

	// OpenTab
	openRes, err := driver.OpenTab(ctx, protocol.OpenParams{URL: "http://example.com"})
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	targetID := openRes.TargetID
	if targetID != "tab-mock-1" {
		t.Fatalf("expected targetId tab-mock-1, got: %s", targetID)
	}

	// Eval
	evalRes, err := driver.Eval(ctx, protocol.EvalParams{TargetID: targetID, Expression: "document.title"})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if evalRes.Value != "evaluated-ok" {
		t.Fatalf("expected evaluated-ok, got: %v", evalRes.Value)
	}

	// Snapshot
	snap, err := driver.Snapshot(ctx, protocol.SnapshotParams{TargetID: targetID, InteractiveOnly: true})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap == nil || len(snap.Nodes) != 1 {
		t.Fatalf("expected 1 node, got: %+v", snap)
	}
	if snap.Nodes[0].Ref != "@e1" || snap.Nodes[0].Name != "Submit" {
		t.Fatalf("unexpected node details: %+v", snap.Nodes[0])
	}
	if snap.RefTable["@e1"] == 0 {
		t.Fatalf("expected ref table to contain @e1")
	}

	// Click
	if err := driver.Click(ctx, protocol.ClickParams{TargetID: targetID, Selector: "@e1"}); err != nil {
		t.Fatalf("Click: %v", err)
	}

	// Fill
	if err := driver.Fill(ctx, protocol.FillParams{TargetID: targetID, Selector: "@e1", Text: "test-text"}); err != nil {
		t.Fatalf("Fill: %v", err)
	}

	// Type
	if err := driver.Type(ctx, protocol.TypeParams{TargetID: targetID, Selector: "@e1", Text: "more-text"}); err != nil {
		t.Fatalf("Type: %v", err)
	}

	// Press
	if err := driver.Press(ctx, protocol.PressParams{TargetID: targetID, Key: "Enter"}); err != nil {
		t.Fatalf("Press: %v", err)
	}

	// Hover
	if err := driver.Hover(ctx, protocol.HoverParams{TargetID: targetID, Selector: "@e1"}); err != nil {
		t.Fatalf("Hover: %v", err)
	}

	// Focus
	if err := driver.Focus(ctx, protocol.FocusParams{TargetID: targetID, Selector: "@e1"}); err != nil {
		t.Fatalf("Focus: %v", err)
	}

	// Screenshot
	ss, err := driver.Screenshot(ctx, protocol.ScreenshotParams{TargetID: targetID})
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if ss.Base64 != "mock-screenshot-base64" {
		t.Fatalf("unexpected screenshot data: %s", ss.Base64)
	}

	// CloseTab
	if err := driver.CloseTab(ctx, protocol.CloseParams{TargetID: targetID}); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}

	// Eval on closed tab should return ErrTargetNotFound
	_, err = driver.Eval(ctx, protocol.EvalParams{TargetID: targetID, Expression: "1+1"})
	if !errors.Is(err, protocol.ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound on closed tab, got: %v", err)
	}
}
