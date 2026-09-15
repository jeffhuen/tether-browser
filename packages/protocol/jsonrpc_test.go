package protocol

import (
	"testing"
)

func TestJSONRPCRequest(t *testing.T) {
	type SampleParams struct {
		URL string `json:"url"`
	}

	req, err := NewRequest("req-1", "browser.open", SampleParams{URL: "https://example.com"}, 42, "epoch-a")
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	if req.JSONRPC != JSONRPCVersion {
		t.Errorf("expected jsonrpc %s, got %s", JSONRPCVersion, req.JSONRPC)
	}
	if req.ID != "req-1" {
		t.Errorf("expected id req-1, got %s", req.ID)
	}
	if req.Seq != 42 {
		t.Errorf("expected seq 42, got %d", req.Seq)
	}
	if req.Epoch != "epoch-a" {
		t.Errorf("expected epoch epoch-a, got %s", req.Epoch)
	}

	var unpacked SampleParams
	if err := req.UnmarshalParams(&unpacked); err != nil {
		t.Fatalf("failed to unmarshal params: %v", err)
	}
	if unpacked.URL != "https://example.com" {
		t.Errorf("expected URL https://example.com, got %s", unpacked.URL)
	}
}

func TestJSONRPCResponse(t *testing.T) {
	type SampleResult struct {
		Title string `json:"title"`
	}

	resp, err := NewResponse("req-1", SampleResult{Title: "Home"}, 42, "epoch-a")
	if err != nil {
		t.Fatalf("failed to create response: %v", err)
	}

	var unpacked SampleResult
	if err := resp.UnmarshalResult(&unpacked); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if unpacked.Title != "Home" {
		t.Errorf("expected title Home, got %s", unpacked.Title)
	}
}

func TestJSONRPCErrorResponse(t *testing.T) {
	resp := NewErrorResponse("req-2", CodeStaleRef, ErrStaleRef.Error(), map[string]string{"ref": "@e4"}, 43, "epoch-a")

	var unpacked struct{}
	err := resp.UnmarshalResult(&unpacked)
	if err == nil {
		t.Fatalf("expected error from UnmarshalResult, got nil")
	}

	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T", err)
	}
	if rpcErr.Code != CodeStaleRef {
		t.Errorf("expected code %d, got %d", CodeStaleRef, rpcErr.Code)
	}
	if rpcErr.Message != ErrStaleRef.Error() {
		t.Errorf("expected message %s, got %s", ErrStaleRef.Error(), rpcErr.Message)
	}
}
