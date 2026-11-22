// Package mcpclient is a deliberately tiny MCP client for the demo server:
// one stateless JSON-RPC POST per request, with the protocol version in
// both the header and _meta, and the Mcp-Method / Mcp-Name headers the
// 2026-07-28 transport asks for. It exists so you can poke the server and
// so the end-to-end test speaks the same wire format a real client would.
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
)

// Client talks to one MCP endpoint.
type Client struct {
	URL   string
	Token string // bearer token; empty sends no Authorization header
	HTTP  *http.Client

	id atomic.Int64
}

// Response is the raw JSON-RPC answer plus the HTTP status and headers.
type Response struct {
	Status int
	Header http.Header
	Result map[string]any
	Error  *RPCError
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Do sends one request. meta is merged into params._meta.
func (c *Client) Do(ctx context.Context, method string, params map[string]any, meta map[string]any) (Response, error) {
	if params == nil {
		params = map[string]any{}
	}
	m := map[string]any{"io.modelcontextprotocol/protocolVersion": mcp.Version}
	for k, v := range meta {
		m[k] = v
	}
	params["_meta"] = m
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id.Add(1), "method": method, "params": params})
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcp.Version)
	req.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		req.Header.Set("Mcp-Name", name)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	out := Response{Status: resp.StatusCode, Header: resp.Header}
	var env struct {
		Result map[string]any `json:"result"`
		Error  *RPCError      `json:"error"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			return out, fmt.Errorf("mcpclient: bad response (%d): %s", resp.StatusCode, raw)
		}
	}
	out.Result, out.Error = env.Result, env.Error
	return out, nil
}

// CallTool calls tools/call. idemKey, when set, goes into _meta under this
// repo's own idempotency convention.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any, idemKey string) (Response, error) {
	var meta map[string]any
	if idemKey != "" {
		meta = map[string]any{mcp.IdempotencyMetaKey: idemKey}
	}
	return c.Do(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, meta)
}

// ToolNames lists tool names from a tools/list result.
func ToolNames(r Response) []string {
	var names []string
	tools, _ := r.Result["tools"].([]any)
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return names
}

// Text returns the first text content block of a tools/call result, and
// whether the result was a tool error (isError).
func Text(r Response) (string, bool) {
	isErr, _ := r.Result["isError"].(bool)
	content, _ := r.Result["content"].([]any)
	for _, c := range content {
		if m, ok := c.(map[string]any); ok && m["type"] == "text" {
			s, _ := m["text"].(string)
			return s, isErr
		}
	}
	return "", isErr
}
