// Package jsonrpc holds the JSON-RPC 2.0 envelope that MCP messages
// travel in, plus the error codes a tool server returns.
package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Standard JSON-RPC 2.0 codes, and the codes MCP 2026-07-28 reserves in
// the -32020..-32099 range (checked Nov 2026). -32000..-32019 is left
// to implementations; RateLimited is ours.
const (
	ParseError                 = -32700
	InvalidRequest             = -32600
	MethodNotFound             = -32601
	InvalidParams              = -32602
	InternalError              = -32603
	RateLimited                = -32010
	HeaderMismatch             = -32020
	MissingClientCapability    = -32021
	UnsupportedProtocolVersion = -32022
)

// Request is a JSON-RPC request or, when ID is empty, a notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request expects no response.
func (r Request) IsNotification() bool { return len(r.ID) == 0 }

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Response carries either a result or an error, never both.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Result builds a success response.
func Result(id json.RawMessage, v any) Response {
	return Response{JSONRPC: "2.0", ID: idOrNull(id), Result: v}
}

// Fail builds an error response.
func Fail(id json.RawMessage, code int, msg string, data any) Response {
	return Response{JSONRPC: "2.0", ID: idOrNull(id), Error: &Error{Code: code, Message: msg, Data: data}}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// Parse decodes one request and checks the envelope. MCP request IDs
// must be a string or a number; null is not allowed.
func Parse(body []byte) (Request, *Error) {
	var r Request
	if err := json.Unmarshal(body, &r); err != nil {
		return r, &Error{Code: ParseError, Message: "parse error"}
	}
	if r.JSONRPC != "2.0" || r.Method == "" {
		return r, &Error{Code: InvalidRequest, Message: "not a JSON-RPC 2.0 request"}
	}
	if err := checkID(r.ID); err != nil {
		return r, &Error{Code: InvalidRequest, Message: err.Error()}
	}
	return r, nil
}

func checkID(id json.RawMessage) error {
	id = bytes.TrimSpace(id)
	if len(id) == 0 {
		return nil
	}
	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return nil
	default:
		return errors.New("id must be a string or a number")
	}
}
