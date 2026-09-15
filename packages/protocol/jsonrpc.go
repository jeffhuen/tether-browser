package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// JSONRPCVersion is the supported protocol version.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Tether-specific application error codes.
const (
	CodeTargetNotFound  = -32000
	CodeStaleRef        = -32001
	CodeActionTimeout   = -32002
	CodeNotActionable   = -32003
	CodeNavigationError = -32004
	CodeAuthRequired    = -32005
)

// Standard error sentinels.
var (
	ErrTargetNotFound  = errors.New("target not found or tab closed")
	ErrStaleRef        = errors.New("stale element reference: node no longer in document")
	ErrActionTimeout   = errors.New("action timeout waiting for condition")
	ErrNotActionable   = errors.New("element not actionable: hidden, covered, or disabled")
	ErrNavigationError = errors.New("navigation failed")
	ErrAuthRequired    = errors.New("authentication or approval required")
)

// Request represents a JSON-RPC 2.0 request with session tracking.
// ID can be a JSON string, number, or null.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Seq     uint64          `json:"seq,omitempty"`
	Epoch   string          `json:"epoch,omitempty"`
}

// Response represents a JSON-RPC 2.0 response with session tracking.
// For successful responses, Result is never omitted (emits "null" if nil).
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	Seq     uint64          `json:"seq,omitempty"`
	Epoch   string          `json:"epoch,omitempty"`
}

// Notification represents a one-way notification without an ID.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError represents a structured JSON-RPC 2.0 error.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("rpc error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// FormatID formats a string, int, or raw bytes into a JSON-RPC raw ID.
func FormatID(id any) json.RawMessage {
	if id == nil {
		return json.RawMessage("null")
	}
	switch v := id.(type) {
	case string:
		if v == "" || v == "null" {
			return json.RawMessage("null")
		}
		return json.RawMessage(strconv.Quote(v))
	case int:
		return json.RawMessage(strconv.Itoa(v))
	case int64:
		return json.RawMessage(strconv.FormatInt(v, 10))
	case uint64:
		return json.RawMessage(strconv.FormatUint(v, 10))
	case json.RawMessage:
		if len(v) == 0 {
			return json.RawMessage("null")
		}
		return v
	default:
		d, err := json.Marshal(v)
		if err != nil {
			return json.RawMessage("null")
		}
		return d
	}
}

// IDString extracts the string representation of an ID.
func IDString(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// NewRequest creates a typed request with automatic JSON marshaling of params.
func NewRequest(id any, method string, params any, seq uint64, epoch string) (*Request, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal request params: %w", err)
		}
		raw = data
	}
	return &Request{
		JSONRPC: JSONRPCVersion,
		ID:      FormatID(id),
		Method:  method,
		Params:  raw,
		Seq:     seq,
		Epoch:   epoch,
	}, nil
}

// NewResponse creates a successful response with automatic JSON marshaling of result.
// If result is nil, it serializes as literal JSON null per JSON-RPC 2.0 spec.
func NewResponse(id any, result any, seq uint64, epoch string) (*Response, error) {
	raw := json.RawMessage("null")
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal response result: %w", err)
		}
		raw = data
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      FormatID(id),
		Result:  raw,
		Seq:     seq,
		Epoch:   epoch,
	}, nil
}

// NewErrorResponse creates an error response for a failed request.
// Per JSON-RPC 2.0, Result is omitted on error responses.
func NewErrorResponse(id any, code int, message string, data any, seq uint64, epoch string) *Response {
	var rawData json.RawMessage
	if data != nil {
		if d, err := json.Marshal(data); err == nil {
			rawData = d
		}
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      FormatID(id),
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    rawData,
		},
		Seq:   seq,
		Epoch: epoch,
	}
}

// UnmarshalParams unpacks raw request parameters into the target destination.
func (r *Request) UnmarshalParams(dest any) error {
	if len(r.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Params, dest); err != nil {
		return fmt.Errorf("unmarshal params: %w", err)
	}
	return nil
}

// UnmarshalResult unpacks raw response results into the target destination.
func (r *Response) UnmarshalResult(dest any) error {
	if r.Error != nil {
		return r.Error
	}
	if len(r.Result) == 0 || bytes.Equal(r.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(r.Result, dest); err != nil {
		return fmt.Errorf("unmarshal result: %w", err)
	}
	return nil
}
