// Package mcp is a small MCP tool server over Streamable HTTP. It
// follows the shapes of MCP revision 2026-07-28 (checked Nov 2026):
// stateless requests with _meta, server/discover, tools/list,
// tools/call, header/body checks, and a minimal legacy initialize for
// 2025-11-25 clients. It implements tools only: no resources, prompts,
// SSE streams or subscriptions.
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/audit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/auth"
	"github.com/AlwyAbdullah/the-tool-layer/internal/idem"
	"github.com/AlwyAbdullah/the-tool-layer/internal/jsonrpc"
	"github.com/AlwyAbdullah/the-tool-layer/internal/ratelimit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
)

// Protocol versions this server speaks.
const (
	Version       = "2026-07-28"
	LegacyVersion = "2025-11-25"
	maxBody       = 1 << 20
)

// Call is one tool invocation as a handler sees it. The principal is in
// ctx.
type Call struct {
	Tool string
	Args json.RawMessage
}

// ToolResult is what a handler returns. IsError results are business
// outcomes the model can act on ("order not found"); Go errors are
// faults.
type ToolResult struct {
	Text       string   `json:"text"`
	Structured any      `json:"structured,omitempty"`
	IsError    bool     `json:"isError,omitempty"`
	Findings   []string `json:"findings,omitempty"`
}

// Handler runs one tool.
type Handler func(ctx context.Context, c Call) (ToolResult, error)

// Server is the HTTP handler for the MCP endpoint.
type Server struct {
	Name, Version, Instructions string

	Registry    *toolspec.Registry
	Handlers    map[string]Handler
	Auth        auth.Validator
	MetadataURL string
	Implies     map[string][]string
	Origins     []string // allowed browser origins; requests without Origin pass

	Limiter *ratelimit.Limiter
	Quota   *ratelimit.Quota
	Meter   *ratelimit.Meter
	Idem    *idem.Store
	Audit   *audit.Logger
	Now     func() time.Time
}

// ServeHTTP handles one POST to the MCP endpoint.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed) // no GET stream, no DELETE session
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !slices.Contains(s.Origins, o) {
		w.WriteHeader(http.StatusForbidden) // DNS rebinding protection
		return
	}
	p, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	req, perr := jsonrpc.Parse(body)
	if perr != nil {
		writeJSON(w, http.StatusBadRequest, jsonrpc.Fail(req.ID, perr.Code, perr.Message, nil))
		return
	}
	if req.IsNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	status, resp := s.handle(tenant.With(r.Context(), p), w, r, req)
	writeJSON(w, status, resp)
}

// authenticate turns a valid bearer token into a principal, or answers
// 401 with a challenge that points at the protected resource metadata.
func (s *Server) authenticate(w http.ResponseWriter,
	r *http.Request) (tenant.Principal, bool) {
	tok, err := auth.BearerToken(r)
	if err == nil {
		c, verr := s.Auth.Validate(tok)
		if verr == nil {
			return tenant.Principal{Tenant: c.Tenant,
				Subject: c.Subject, ClientID: c.ClientID,
				Scopes: c.Scopes()}, true
		}
	}
	code := ""
	if err == nil {
		code = "invalid_token"
	}
	auth.Challenge(w.Header(), s.MetadataURL, code, "")
	w.WriteHeader(http.StatusUnauthorized)
	return tenant.Principal{}, false
}

func (s *Server) handle(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonrpc.Request) (int, jsonrpc.Response) {
	if req.Method == "initialize" {
		return http.StatusOK, jsonrpc.Result(req.ID, s.initializeLegacy())
	}
	if r.Header.Get("MCP-Protocol-Version") != LegacyVersion {
		if e := checkHeaders(r, req); e != nil {
			return http.StatusBadRequest, jsonrpc.Fail(req.ID, e.Code, e.Message, e.Data)
		}
	}
	switch req.Method {
	case "server/discover":
		return http.StatusOK, jsonrpc.Result(req.ID, s.discover())
	case "tools/list":
		return http.StatusOK, jsonrpc.Result(req.ID, s.list(ctx))
	case "tools/call":
		return s.call(ctx, w, req)
	}
	return http.StatusNotFound, jsonrpc.Fail(req.ID, jsonrpc.MethodNotFound, "method not found: "+req.Method, nil)
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"io.modelcontextprotocol/serverInfo": map[string]string{"name": s.Name, "version": s.Version}}
}

func (s *Server) discover() map[string]any {
	return map[string]any{
		"resultType":        "complete",
		"supportedVersions": []string{Version, LegacyVersion},
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"instructions":      s.Instructions,
		"ttlMs":             3600000,
		"cacheScope":        "public",
		"_meta":             s.serverInfo(),
	}
}

// initializeLegacy answers the 2025-11-25 handshake. No session is
// minted: later legacy requests are served statelessly like modern
// ones.
func (s *Server) initializeLegacy() map[string]any {
	return map[string]any{
		"protocolVersion": LegacyVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]string{"name": s.Name, "version": s.Version},
		"instructions":    s.Instructions,
	}
}

// list returns only the tools this token's scopes allow. Because the
// answer depends on the caller, it may be cached only per authorization
// context.
func (s *Server) list(ctx context.Context) map[string]any {
	p, _ := tenant.From(ctx)
	allowed := func(scope string) bool {
		return auth.HasScope(p.Scopes, scope, s.Implies)
	}
	return map[string]any{
		"resultType": "complete",
		"tools":      s.Registry.List(allowed, s.now()),
		"ttlMs":      300000,
		"cacheScope": "private",
		"_meta":      s.serverInfo(),
	}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
