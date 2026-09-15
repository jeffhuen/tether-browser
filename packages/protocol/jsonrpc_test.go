package protocol

import (
	"encoding/json"
	"strings"
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
	if IDString(req.ID) != "req-1" {
		t.Errorf("expected id req-1, got %s", IDString(req.ID))
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

func TestJSONRPCNumericAndNullIDs(t *testing.T) {
	// 1. Numeric ID
	req1, err := NewRequest(105, "browser.status", nil, 1, "epoch-a")
	if err != nil {
		t.Fatalf("failed to create numeric ID request: %v", err)
	}
	if string(req1.ID) != "105" {
		t.Errorf("expected ID 105, got %s", string(req1.ID))
	}

	rawJSON, err := json.Marshal(req1)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if !strings.Contains(string(rawJSON), `"id":105`) {
		t.Errorf("expected raw json to contain numeric id: %s", string(rawJSON))
	}

	// 2. Null ID for unrecoverable errors
	errResp := NewErrorResponse(nil, CodeParseError, "Parse error", nil, 0, "")
	if string(errResp.ID) != "null" {
		t.Errorf("expected null ID for parse error, got %s", string(errResp.ID))
	}
	errJSON, err := json.Marshal(errResp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if !strings.Contains(string(errJSON), `"id":null`) {
		t.Errorf("expected raw json to contain null id: %s", string(errJSON))
	}
}

func TestJSONRPCResponseNilResultEmitsLiteralNull(t *testing.T) {
	resp, err := NewResponse("req-1", nil, 1, "epoch-a")
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	// JSON-RPC 2.0 requires "result":null on success when result is nil
	if !strings.Contains(string(raw), `"result":null`) {
		t.Errorf("expected JSON-RPC response to contain 'result':null, got: %s", string(raw))
	}

	var unpacked struct{}
	if err := resp.UnmarshalResult(&unpacked); err != nil {
		t.Errorf("unmarshal nil result should succeed without error, got: %v", err)
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
