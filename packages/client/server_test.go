package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jeffhuen/tether-browser/packages/protocol"
	"net/http"
	"testing"
	"time"
)

type mockDriver struct {
	BrowserDriver
}

func (m *mockDriver) Status(ctx context.Context, p protocol.StatusParams) (*protocol.StatusResult, error) {
	return &protocol.StatusResult{
		Connected:   true,
		TargetCount: 1,
		Version:     "1.0.0",
	}, nil
}
func TestServerHTTPSecurity(t *testing.T) {
	server := NewServer(&mockDriver{})
	server.SetAuthToken("test-bearer-secret")
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe(0) }()
	t.Cleanup(func() {
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for server.Port() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.Port() == 0 {
		t.Fatal("server failed to bind")
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", server.Port())
	client := &http.Client{Timeout: 3 * time.Second}
	reqObj, _ := protocol.NewRequest("req-auth", protocol.MethodStatus, nil, 1, "")
	body, _ := json.Marshal(reqObj)
	for _, tc := range []struct {
		name, origin, contentType, token string
		want                             int
	}{
		{"external origin", "http://malicious-site.com", "application/json", "test-bearer-secret", http.StatusForbidden},
		{"wrong content type", "", "text/plain", "test-bearer-secret", http.StatusUnsupportedMediaType},
		{"missing token", "", "application/json", "", http.StatusUnauthorized},
		{"wrong token", "", "application/json", "wrong-token", http.StatusUnauthorized},
		{"valid token", "", "application/json", "test-bearer-secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Close = true
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", tc.contentType)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("expected HTTP %d, got %d", tc.want, resp.StatusCode)
			}
		})
	}
}
