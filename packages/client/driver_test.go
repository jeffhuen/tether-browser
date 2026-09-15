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
		resJSON := fmt.Sprintf(`{"id":"tab-mock-1","title":"Test Tab","url":"http://example.com","webSocketDebuggerUrl":"ws://%s/devtools/page/tab-mock-1"}`, serverAddr)
		resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(resJSON), resJSON)
		_, _ = conn.Write([]byte(resp))
		return
	}

	// Handle /json/close
	if strings.HasPrefix(req.URL.Path, "/json/close") {
		resp := "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"
		_, _ = conn.Write([]byte(resp))
		return
	}

	// Handle WebSocket Upgrade
	if strings.ToLower(req.Header.Get("Upgrade")) == "websocket" {
		secKey := req.Header.Get("Sec-WebSocket-Key")
		h := sha1.New()
		h.Write([]byte(secKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

		wsUpgradeResp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey)
		if _, err := conn.Write([]byte(wsUpgradeResp)); err != nil {
			return
		}

		// Read client frames and respond
		for {
			header := make([]byte, 2)
			if _, err := io.ReadFull(br, header); err != nil {
				return
			}

			payloadLen := uint64(header[1] & 0x7F)
			if payloadLen == 126 {
				ext := make([]byte, 2)
				_, _ = io.ReadFull(br, ext)
				payloadLen = uint64(binary.BigEndian.Uint16(ext))
			} else if payloadLen == 127 {
				ext := make([]byte, 8)
				_, _ = io.ReadFull(br, ext)
				payloadLen = binary.BigEndian.Uint64(ext)
			}

			mask := make([]byte, 4)
			_, _ = io.ReadFull(br, mask)

			payload := make([]byte, payloadLen)
			_, _ = io.ReadFull(br, payload)
			for i := range payloadLen {
				payload[i] ^= mask[i%4]
			}

			var cdpMsg struct {
				ID     uint64          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(payload, &cdpMsg); err != nil {
				continue
			}

			var resultJSON string
			switch cdpMsg.Method {
			case "Page.enable", "Runtime.enable", "DOM.enable":
				resultJSON = "{}"
			case "Page.captureScreenshot":
				resultJSON = `{"data":"mock-screenshot-base64"}`
			case "Runtime.evaluate":
				paramStr := string(cdpMsg.Params)
				if strings.Contains(paramStr, "interactiveOnly") {
					// Snapshot script
					snapshotData := `{"title":"Snapshot Page","url":"http://test.local","nodes":[{"ref":"@e1","backendNodeId":1,"role":"button","name":"Submit","isInteractive":true,"rect":{"x":10,"y":10,"width":100,"height":30}}]}`
					escaped, _ := json.Marshal(snapshotData)
					resultJSON = fmt.Sprintf(`{"result":{"type":"string","value":%s}}`, string(escaped))
				} else if strings.Contains(paramStr, "notFound") {
					resultJSON = `{"result":{"type":"object","value":{"ok":true}}}`
				} else {
					resultJSON = `{"result":{"type":"string","value":"evaluated-ok"}}`
				}
			default:
				resultJSON = "{}"
			}

			responsePayload := fmt.Sprintf(`{"id":%d,"result":%s}`, cdpMsg.ID, resultJSON)
			sendServerWSFrame(conn, []byte(responsePayload))
		}
	}
}

func sendServerWSFrame(conn net.Conn, payload []byte) {
	length := len(payload)
	var header []byte
	if length < 126 {
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
	targetID, err := driver.OpenTab(ctx, "http://example.com")
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	if targetID != "tab-mock-1" {
		t.Fatalf("expected targetId tab-mock-1, got: %s", targetID)
	}

	// Eval
	val, err := driver.Eval(ctx, targetID, "document.title")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if val != "evaluated-ok" {
		t.Fatalf("expected evaluated-ok, got: %v", val)
	}

	// Snapshot
	snap, err := driver.Snapshot(ctx, targetID, true)
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
	if err := driver.Click(ctx, targetID, "@e1"); err != nil {
		t.Fatalf("Click: %v", err)
	}

	// Fill
	if err := driver.Fill(ctx, targetID, "@e1", "test-text"); err != nil {
		t.Fatalf("Fill: %v", err)
	}

	// Type
	if err := driver.Type(ctx, targetID, "@e1", "more-text"); err != nil {
		t.Fatalf("Type: %v", err)
	}

	// Press
	if err := driver.Press(ctx, targetID, "Enter"); err != nil {
		t.Fatalf("Press: %v", err)
	}

	// Hover
	if err := driver.Hover(ctx, targetID, "@e1"); err != nil {
		t.Fatalf("Hover: %v", err)
	}

	// Focus
	if err := driver.Focus(ctx, targetID, "@e1"); err != nil {
		t.Fatalf("Focus: %v", err)
	}

	// Screenshot
	ss, err := driver.Screenshot(ctx, targetID, false)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if ss.Base64 != "mock-screenshot-base64" {
		t.Fatalf("unexpected screenshot data: %s", ss.Base64)
	}

	// Review methods
	if err := driver.StartReview(ctx, targetID); err != nil {
		t.Fatalf("StartReview: %v", err)
	}

	// CloseTab
	if err := driver.CloseTab(ctx, targetID); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}

	// Eval on closed tab should return ErrTargetNotFound
	_, err = driver.Eval(ctx, targetID, "1+1")
	if !errors.Is(err, protocol.ErrTargetNotFound) {
		t.Fatalf("expected ErrTargetNotFound on closed tab, got: %v", err)
	}
}
