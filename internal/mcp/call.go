package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/audit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/auth"
	"github.com/AlwyAbdullah/the-tool-layer/internal/idem"
	"github.com/AlwyAbdullah/the-tool-layer/internal/jsonrpc"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
)

// IdempotencyMetaKey is this server's own _meta key (not part of the
// MCP spec) for a client-chosen idempotency key on side-effecting
// calls.
const IdempotencyMetaKey = "co.kiosk/idempotencyKey"

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      struct {
		TraceParent string `json:"traceparent"`
		IdemKey     string `json:"co.kiosk/idempotencyKey"`
	} `json:"_meta"`
}

// call is the tool-call pipeline: resolve, authorize, limit, execute
// once, record. Each step can stop the call with the right kind of
// error.
func (s *Server) call(ctx context.Context, w http.ResponseWriter, req jsonrpc.Request) (int, jsonrpc.Response) {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		return http.StatusOK, jsonrpc.Fail(req.ID, jsonrpc.InvalidParams, "invalid tools/call params", nil)
	}
	who, _ := tenant.From(ctx)
	ev := audit.Event{Time: s.now(),
		TraceID: audit.TraceID(p.Meta.TraceParent),
		Tenant:  who.Tenant, Subject: who.Subject, Client: who.ClientID,
		Tool: p.Name, ArgsHash: audit.HashArgs(p.Arguments)}
	defer func(start time.Time) {
		ev.DurationMS = s.now().Sub(start).Milliseconds()
		s.record(ev)
	}(s.now())

	spec, warning, err := s.Registry.Resolve(p.Name, s.now())
	if errors.Is(err, toolspec.ErrUnknown) {
		ev.Outcome = audit.Denied
		return http.StatusOK, jsonrpc.Fail(req.ID, jsonrpc.InvalidParams, "Unknown tool: "+p.Name, nil)
	}
	ev.ToolVer = spec.Version
	if err != nil { // retired: tell the model what to use instead
		ev.Outcome = audit.ToolError
		return http.StatusOK, s.result(req.ID, ev.TraceID, ToolResult{Text: warning, IsError: true}, "")
	}
	if status, resp, stop := s.admit(w, req, who, spec, &ev); stop {
		return status, resp
	}
	res, replayed, err := s.execute(ctx, spec, p)
	switch {
	case err != nil:
		ev.Outcome, ev.Detail = audit.ToolError, err.Error()
		res = ToolResult{Text: "The tool failed (trace " + ev.TraceID + "). Check state before retrying.", IsError: true}
	case replayed:
		ev.Outcome = audit.Replayed
	case res.IsError:
		ev.Outcome = audit.ToolError
	default:
		ev.Outcome = audit.OK
	}
	ev.Findings = res.Findings
	return http.StatusOK, s.result(req.ID, ev.TraceID, res, warning)
}

// admit runs the checks that happen before any work: scope, burst
// limit, daily quota. It returns stop=true with the response to send.
func (s *Server) admit(w http.ResponseWriter, req jsonrpc.Request, who tenant.Principal, spec toolspec.Spec, ev *audit.Event) (int, jsonrpc.Response, bool) {
	if !auth.HasScope(who.Scopes, spec.Scope, s.Implies) {
		ev.Outcome = audit.Denied
		auth.Challenge(w.Header(), s.MetadataURL,
			"insufficient_scope", spec.Scope)
		msg := "insufficient scope: " + spec.Scope
		return http.StatusForbidden,
			jsonrpc.Fail(req.ID, jsonrpc.InvalidRequest, msg, nil), true
	}
	if ok, wait := s.Limiter.Allow(who.Tenant+"|"+who.ClientID, spec.Cost); !ok {
		ev.Outcome = audit.RateLimited
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		return http.StatusTooManyRequests, jsonrpc.Fail(req.ID, jsonrpc.RateLimited, "rate limited",
			map[string]int64{"retryAfterMs": wait.Milliseconds()}), true
	}
	if _, ok := s.Quota.Spend(who.Tenant, int64(spec.Cost)); !ok {
		ev.Outcome = audit.RateLimited
		msg := "Daily tool quota for this account is used up. " +
			"It resets at 00:00 UTC; tell the user."
		res := s.result(req.ID, ev.TraceID,
			ToolResult{Text: msg, IsError: true}, "")
		return http.StatusOK, res, true
	}
	if s.Meter != nil {
		s.Meter.Add(who.Tenant, who.ClientID, spec.Name, int64(spec.Cost))
	}
	return 0, jsonrpc.Response{}, false
}

// execute runs the handler, at most once per idempotency key when the
// client sent one for a tool that changes state.
func (s *Server) execute(ctx context.Context, spec toolspec.Spec, p callParams) (ToolResult, bool, error) {
	h := s.Handlers[spec.Name]
	run := func() (ToolResult, error) { return h(ctx, Call{Tool: spec.Name, Args: p.Arguments}) }
	ro := spec.Annotations.ReadOnlyHint
	if p.Meta.IdemKey == "" || (ro != nil && *ro) || s.Idem == nil {
		res, err := run()
		return res, false, err
	}
	fp, err := idem.Fingerprint(p.Arguments)
	if err != nil {
		return ToolResult{Text: "arguments are not valid JSON", IsError: true}, false, nil
	}
	who, _ := tenant.From(ctx)
	raw, replayed, err := s.Idem.Do(idem.Key(who.Tenant, spec.Name, p.Meta.IdemKey), fp, func() ([]byte, error) {
		res, err := run()
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
	if errors.Is(err, idem.ErrConflict) || errors.Is(err, idem.ErrInProgress) {
		return ToolResult{Text: err.Error(), IsError: true}, false, nil
	}
	if err != nil {
		return ToolResult{}, false, err
	}
	var res ToolResult
	return res, replayed, json.Unmarshal(raw, &res)
}

// result builds the CallToolResult. Structured data is also serialized
// into a text block for clients that only read text.
func (s *Server) result(id json.RawMessage, traceID string, r ToolResult, warning string) jsonrpc.Response {
	content := []map[string]string{{"type": "text", "text": r.Text}}
	if r.Structured != nil {
		b, _ := json.Marshal(r.Structured)
		content = append(content, map[string]string{"type": "text", "text": string(b)})
	}
	if warning != "" {
		content = append(content, map[string]string{"type": "text", "text": "Note: " + warning})
	}
	meta := s.serverInfo()
	meta["co.kiosk/traceId"] = traceID
	out := map[string]any{"resultType": "complete", "content": content, "isError": r.IsError, "_meta": meta}
	if r.Structured != nil {
		out["structuredContent"] = r.Structured
	}
	return jsonrpc.Result(id, out)
}

func (s *Server) record(e audit.Event) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.Record(e); err != nil {
		fmt.Println("audit write failed:", err) // never drop silently
	}
}
