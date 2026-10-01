//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/client"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestOfflineScreenshotNativeProcessWithActiveBridge(t *testing.T) {
	if os.Getenv("TETHER_TEST_OFFLINE_NATIVE") == "1" {
		os.Exit(runNativeHost(nil))
	}
	dir, shot := legacyScreenshotCache(t)
	socket := filepath.Join(t.TempDir(), "native", "bridge.sock")
	t.Setenv("TETHER_BRIDGE_SOCKET", socket)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input, chrome := io.Pipe()
	defer input.Close()
	defer chrome.Close()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- client.RunNativeHostServer(ctx, input, io.Discard, []byte(`{"type":"heartbeat"}`), nil)
	}()
	for {
		if conn, err := net.Dial("unix", socket); err == nil {
			conn.Close()
			break
		}
		select {
		case err := <-serverDone:
			t.Fatalf("automation bridge exited: %v", err)
		case <-ctx.Done():
			t.Fatal("automation bridge did not become ready")
		case <-time.After(10 * time.Millisecond):
		}
	}

	for _, kind := range []string{"system_delete_screenshot", "system_clear_screenshots"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := os.Stat(shot); os.IsNotExist(err) {
				if err := protocol.WritePrivateFile(shot, []byte("local image")); err != nil {
					t.Fatal(err)
				}
			}
			payload, _ := json.Marshal(map[string]any{
				"id": kind, "type": kind, "filename": filepath.Base(shot), "mirrored": false,
				"screenshots": []map[string]any{{"filename": filepath.Base(shot), "mirrored": false}},
			})
			var request bytes.Buffer
			if err := client.WriteNativeMessage(&request, payload); err != nil {
				t.Fatal(err)
			}
			processCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			cmd := exec.CommandContext(processCtx, os.Args[0], "-test.run=^TestOfflineScreenshotNativeProcessWithActiveBridge$")
			cmd.Env = append(os.Environ(), "TETHER_TEST_OFFLINE_NATIVE=1")
			cmd.Stdin = &request
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("offline native process failed with automation active: %v", err)
			}
			reply, err := client.ReadNativeMessage(bytes.NewReader(output))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Result struct {
					OK, LocalDeleted, RemoteDeleted bool
				} `json:"result"`
				Error any `json:"error"`
			}
			if err := json.Unmarshal(reply, &response); err != nil || response.ID != kind || response.Type != "response" || response.Error != nil || !response.Result.OK || !response.Result.LocalDeleted || !response.Result.RemoteDeleted {
				t.Fatalf("incorrect offline cleanup response: %s (%v)", reply, err)
			}
			if _, err := os.Stat(shot); !os.IsNotExist(err) {
				t.Fatalf("offline cleanup left screenshot: %v", err)
			}
			if err := protocol.ValidatePrivateDir(dir); err != nil {
				t.Fatal(err)
			}
			conn, err := net.Dial("unix", socket)
			if err != nil {
				t.Fatalf("file process disturbed automation listener: %v", err)
			}
			conn.Close()
		})
	}
}
