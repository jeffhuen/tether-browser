package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	// 3. Empty string ID
	reqEmpty, err := NewRequest("", "browser.status", nil, 1, "epoch-a")
	if err != nil {
		t.Fatalf("create empty string id: %v", err)
	}
	if string(reqEmpty.ID) != `""` {
		t.Errorf("expected empty string ID to be \"\", got: %s", string(reqEmpty.ID))
	}

	// 4. String ID "null"
	reqNullStr, err := NewRequest("null", "browser.status", nil, 1, "epoch-a")
	if err != nil {
		t.Fatalf("create string id 'null': %v", err)
	}
	if string(reqNullStr.ID) != `"null"` {
		t.Errorf("expected string ID 'null' to be \"null\", got: %s", string(reqNullStr.ID))
	}

	// 5. String ID with control character \x00
	reqCtrl, err := NewRequest("\x00", "browser.status", nil, 1, "epoch-a")
	if err != nil {
		t.Fatalf("create control char id: %v", err)
	}
	rawCtrl, err := json.Marshal(reqCtrl)
	if err != nil {
		t.Fatalf("json.Marshal with control char in ID failed: %v", err)
	}
	if !strings.Contains(string(rawCtrl), `\u0000`) {
		t.Errorf("expected JSON-escaped null byte \\u0000, got: %s", string(rawCtrl))
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

	var dest any = "old"
	if err := resp.UnmarshalResult(&dest); err != nil {
		t.Fatalf("unmarshal nil result should succeed, got: %v", err)
	}
	if dest != nil {
		t.Errorf("expected dest to be cleared to nil, got: %v", dest)
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
