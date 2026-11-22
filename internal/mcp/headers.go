package mcp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/AlwyAbdullah/the-tool-layer/internal/jsonrpc"
)

// requestMeta is the part of every modern request the transport checks.
type requestMeta struct {
	Name string `json:"name"`
	Meta struct {
		Version string `json:"io.modelcontextprotocol/protocolVersion"`
	} `json:"_meta"`
}

// checkHeaders enforces the 2026-07-28 rule that the routing headers a
// gateway sees and the body the server executes say the same thing.
func checkHeaders(r *http.Request, req jsonrpc.Request) *jsonrpc.Error {
	var m requestMeta
	json.Unmarshal(req.Params, &m) // bad params fail later, per method
	hv := r.Header.Get("MCP-Protocol-Version")
	switch {
	case hv == "" || r.Header.Get("Mcp-Method") == "":
		return mismatch("missing version or method header")
	case hv != Version:
		return unsupported(hv)
	case m.Meta.Version != hv:
		return mismatch("protocol version header does not match _meta")
	case r.Header.Get("Mcp-Method") != req.Method:
		return mismatch("Mcp-Method header does not match the body")
	case req.Method == "tools/call" &&
		decodeHeader(r.Header.Get("Mcp-Name")) != m.Name:
		return mismatch("Mcp-Name header does not match params.name")
	}
	return nil
}

func unsupported(requested string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.UnsupportedProtocolVersion,
		Message: "Unsupported protocol version",
		Data: map[string]any{"requested": requested,
			"supported": []string{Version, LegacyVersion}}}
}

func mismatch(msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.HeaderMismatch, Message: "Header mismatch: " + msg}
}

// decodeHeader undoes the =?base64?...?= sentinel used for values that
// are not plain ASCII.
func decodeHeader(v string) string {
	if len(v) < 11 || !strings.HasPrefix(v, "=?base64?") || !strings.HasSuffix(v, "?=") {
		return v
	}
	b, err := base64.StdEncoding.DecodeString(v[len("=?base64?") : len(v)-2])
	if err != nil {
		return "\x00invalid"
	}
	return string(b)
}
