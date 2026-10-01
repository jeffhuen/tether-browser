package client

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestLegalNativeScreenshotSurvivesRPCEnvelope(t *testing.T) {
	input, chromeOutput := io.Pipe()
	chromeInput, output := io.Pipe()
	defer input.Close()
	defer chromeOutput.Close()
	defer chromeInput.Close()
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	bridge := NewExtensionBridge(input, output)
	bridge.StartReader(ctx, []byte(`{"type":"heartbeat"}`))
	chromeDone := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			payload, err := ReadNativeMessage(chromeInput)
			if err != nil {
				chromeDone <- err
				return
			}
			var req nativeRequest
			if err := json.Unmarshal(payload, &req); err != nil {
				chromeDone <- err
				return
			}
			id, _ := json.Marshal(req.ID)
			prefix := `{"id":` + string(id) + `,"type":"response","result":`
			var body io.Reader
			var length int
			if i == 0 {
				prefix += `{"base64":"`
				suffix := `","format":"png","width":1,"height":1}}`
				encoded := ((64 << 20) - len(prefix) - len(suffix)) &^ 3
				length = len(prefix) + encoded + len(suffix)
				body = io.MultiReader(strings.NewReader(prefix), io.LimitReader(nativeRepeatedByte{}, int64(encoded)), strings.NewReader(suffix))
			} else {
				prefix += `{"connected":true}}`
				length, body = len(prefix), strings.NewReader(prefix)
			}
			var header [4]byte
			binary.LittleEndian.PutUint32(header[:], uint32(length))
			if _, err := chromeOutput.Write(header[:]); err != nil {
				chromeDone <- err
				return
			}
			if _, err := io.Copy(chromeOutput, body); err != nil {
				chromeDone <- err
				return
			}
		}
		chromeDone <- nil
	}()

	serverConfig, _ := protocol.DaemonTLS("boundary-test-key", true)
	clientConfig, _ := protocol.DaemonTLS("boundary-test-key", false)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(time.Minute))
	_ = b.SetDeadline(time.Now().Add(time.Minute))
	secured := tls.Client(a, clientConfig)
	server := tls.Server(b, serverConfig)
	for _, method := range []string{protocol.MethodScreenshot, protocol.MethodStatus} {
		raw, err := bridge.Call(ctx, method, nil)
		if err != nil {
			t.Fatal(err)
		}
		var result any
		if method == protocol.MethodScreenshot {
			var shot protocol.ScreenshotResult
			if err := json.Unmarshal(raw, &shot); err != nil {
				t.Fatal(err)
			}
			result = shot
		} else {
			var status protocol.StatusResult
			if err := json.Unmarshal(raw, &status); err != nil {
				t.Fatal(err)
			}
			result = status
		}
		req, _ := protocol.NewRequest("consumer-request", method, nil, 17, "native-boundary-session")
		resp, err := protocol.NewResponse(req.ID, result, req.Seq, req.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		writeDone := make(chan error, 1)
		go func() { writeDone <- json.NewEncoder(server).Encode(resp) }()
		got, err := protocol.ReadResponse(secured, req)
		if err != nil {
			t.Fatal(err)
		}
		if err := <-writeDone; err != nil {
			t.Fatal(err)
		}
		if method == protocol.MethodScreenshot {
			var shot protocol.ScreenshotResult
			if err := json.Unmarshal(got.Result, &shot); err != nil {
				t.Fatal(err)
			}
			if len(shot.Base64) < (64<<20)-128 || shot.Base64[0] != 'x' || shot.Format != "png" || shot.Width != 1 || shot.Height != 1 {
				t.Fatal("legal native screenshot changed across typed RPC transport")
			}
		} else {
			var status protocol.StatusResult
			if err := json.Unmarshal(got.Result, &status); err != nil || !status.Connected {
				t.Fatalf("small request after large screenshot failed: %v", err)
			}
		}
	}
	if err := <-chromeDone; err != nil {
		t.Fatal(err)
	}
}
