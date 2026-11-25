package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/audit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/auth"
	"github.com/AlwyAbdullah/the-tool-layer/internal/idem"
	"github.com/AlwyAbdullah/the-tool-layer/internal/jsonrpc"
	kmcp "github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
	"github.com/AlwyAbdullah/the-tool-layer/internal/ratelimit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// checks holds the book's production checks. mcp-go does the protocol
// (discover, tools/list, tools/call, headers, versions); everything that
// decides *who* may do *what*, *how often* and *how many times* stays in
// this file, in three places:
//
//   - gate:    net/http middleware in front of mcp-go (auth, scope, burst limit)
//   - visible: an mcp-go tool filter (tools/list shows only what the token allows)
//   - guard:   an mcp-go tool middleware (quota, idempotency, deprecation, audit)
type checks struct {
	Auth        auth.Validator
	MetadataURL string
	Implies     map[string][]string
	Registry    *toolspec.Registry

	Limiter *ratelimit.Limiter
	Quota   *ratelimit.Quota
	Meter   *ratelimit.Meter
	Idem    *idem.Store
	Audit   *audit.Logger
	Now     func() time.Time
}

const maxBody = 1 << 20

type eventKey struct{}

// eventFrom returns the audit event the gate opened for this tools/call.
func eventFrom(ctx context.Context) *audit.Event {
	if ev, ok := ctx.Value(eventKey{}).(*audit.Event); ok {
		return ev
	}
	return &audit.Event{} // not a gated call: write into a throwaway
}

// carry copies what the gate put on the HTTP request into the context
// mcp-go hands to tool handlers. Use it with server.WithHTTPContextFunc.
func carry(ctx context.Context, r *http.Request) context.Context {
	if p, ok := tenant.From(r.Context()); ok {
		ctx = tenant.With(ctx, p)
	}
	if ev, ok := r.Context().Value(eventKey{}).(*audit.Event); ok {
		ctx = context.WithValue(ctx, eventKey{}, ev)
	}
	return ctx
}

// gate runs before mcp-go sees the request. These checks need HTTP:
// a 401/403 with a WWW-Authenticate challenge (Chapter 4) and a 429
// with Retry-After (Chapter 6) are status codes, which a tool handler
// cannot set.
func (k *checks) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := auth.BearerToken(r)
		var c auth.Claims
		if err == nil {
			c, err = k.Auth.Validate(tok)
		}
		if err != nil {
			code := ""
			if tok != "" {
				code = "invalid_token"
			}
			auth.Challenge(w.Header(), k.MetadataURL, code, "")
			writeJSON(w, http.StatusUnauthorized, jsonrpc.Fail(nil, jsonrpc.InvalidRequest, "unauthorized: "+err.Error(), nil))
			return
		}
		who := tenant.Principal{Tenant: c.Tenant, Subject: c.Subject, ClientID: c.ClientID, Scopes: c.Scopes()}
		ctx := tenant.With(r.Context(), who)

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, jsonrpc.Fail(nil, jsonrpc.InvalidRequest, "request too large", nil))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Meta      struct {
					TraceParent string `json:"traceparent"`
				} `json:"_meta"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &req) != nil || req.Method != "tools/call" {
			next.ServeHTTP(w, r.WithContext(ctx)) // mcp-go answers (or rejects) it
			return
		}

		// One audit record per tools/call, whoever stops it.
		ev := &audit.Event{Time: k.now(), TraceID: audit.TraceID(req.Params.Meta.TraceParent),
			Tenant: who.Tenant, Subject: who.Subject, Client: who.ClientID,
			Tool: req.Params.Name, ArgsHash: audit.HashArgs(req.Params.Arguments), Outcome: audit.Denied}
		defer func(start time.Time) {
			ev.DurationMS = k.now().Sub(start).Milliseconds()
			if err := k.Audit.Record(*ev); err != nil {
				log.Println("audit write failed:", err) // never drop silently
			}
		}(k.now())

		spec, _, err := k.Registry.Resolve(req.Params.Name, k.now())
		if err == nil { // unknown and retired tools are answered further in
			ev.ToolVer = spec.Version
			if !auth.HasScope(who.Scopes, spec.Scope, k.Implies) {
				auth.Challenge(w.Header(), k.MetadataURL, "insufficient_scope", spec.Scope)
				writeJSON(w, http.StatusForbidden, jsonrpc.Fail(req.ID, jsonrpc.InvalidRequest, "insufficient scope: "+spec.Scope, nil))
				return
			}
			if ok, wait := k.Limiter.Allow(who.Tenant+"|"+who.ClientID, spec.Cost); !ok {
				ev.Outcome = audit.RateLimited
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
				writeJSON(w, http.StatusTooManyRequests, jsonrpc.Fail(req.ID, jsonrpc.RateLimited, "rate limited",
					map[string]int64{"retryAfterMs": wait.Milliseconds()}))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, eventKey{}, ev)))
	})
}

// visible is an mcp-go tool filter: a token sees only the tools its
// scopes allow, and retired tools disappear (Chapters 4 and 8). mcp-go
// applies it to tools/call as well, so a hidden tool can't be called.
func (k *checks) visible(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	who, ok := tenant.From(ctx)
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		spec, _, err := k.Registry.Resolve(t.Name, k.now())
		if ok && err == nil && auth.HasScope(who.Scopes, spec.Scope, k.Implies) {
			out = append(out, t)
		}
	}
	return out
}

// guard is mcp-go tool middleware: it runs after the gate and around
// the handler, so it sees the parsed call and the handler's result.
func (k *checks) guard(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ev := eventFrom(ctx)
		who, ok := tenant.From(ctx)
		if !ok {
			return nil, tenant.ErrNoPrincipal // the gate always sets one
		}
		spec, warning, err := k.Registry.Resolve(req.Params.Name, k.now())
		if err != nil {
			ev.Outcome = audit.ToolError
			return toolError(warning), nil // retired: say what to use instead
		}
		if _, ok := k.Quota.Spend(who.Tenant, int64(spec.Cost)); !ok {
			ev.Outcome = audit.RateLimited
			return toolError("Daily tool quota for this account is used up. It resets at 00:00 UTC; tell the user."), nil
		}
		k.Meter.Add(who.Tenant, who.ClientID, spec.Name, int64(spec.Cost))

		res, replayed, err := k.once(ctx, spec, req, next)
		switch {
		case err != nil:
			ev.Outcome, ev.Detail = audit.ToolError, err.Error()
			res = toolError("The tool failed (trace " + ev.TraceID + "). Check state before retrying.")
		case replayed:
			ev.Outcome = audit.Replayed
		case res.IsError:
			ev.Outcome = audit.ToolError
		default:
			ev.Outcome = audit.OK
		}
		ev.Findings = takeFindings(res)
		if warning != "" {
			res.Content = append(res.Content, mcp.NewTextContent("Note: "+warning))
		}
		res.Meta = mcp.NewMetaFromMap(map[string]any{"co.kiosk/traceId": ev.TraceID})
		return res, nil
	}
}

// once runs a state-changing call at most once per idempotency key
// (Chapter 7). The key travels in _meta under this repo's own
// convention; MCP does not define one.
func (k *checks) once(ctx context.Context, spec toolspec.Spec, req mcp.CallToolRequest,
	next server.ToolHandlerFunc) (*mcp.CallToolResult, bool, error) {
	key := ""
	if req.Params.Meta != nil {
		key, _ = req.Params.Meta.AdditionalFields[kmcp.IdempotencyMetaKey].(string)
	}
	if ro := spec.Annotations.ReadOnlyHint; key == "" || (ro != nil && *ro) {
		res, err := next(ctx, req)
		return res, false, err
	}
	fp, err := idem.Fingerprint(rawArgs(req))
	if err != nil {
		return toolError("arguments are not valid JSON"), false, nil
	}
	who, _ := tenant.From(ctx)
	raw, replayed, err := k.Idem.Do(idem.Key(who.Tenant, spec.Name, key), fp, func() ([]byte, error) {
		res, err := next(ctx, req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
	if errors.Is(err, idem.ErrConflict) || errors.Is(err, idem.ErrInProgress) {
		return toolError(err.Error()), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var res mcp.CallToolResult
	return &res, replayed, json.Unmarshal(raw, &res)
}

func (k *checks) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// rawArgs returns the arguments exactly as the client sent them.
func rawArgs(req mcp.CallToolRequest) json.RawMessage {
	if len(req.Params.RawArguments) > 0 {
		return req.Params.RawArguments
	}
	b, _ := json.Marshal(req.Params.Arguments)
	return b
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(text)}, IsError: true}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
