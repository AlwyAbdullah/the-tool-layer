package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/auth"
	"github.com/AlwyAbdullah/the-tool-layer/internal/mcpclient"
)

// The same end-to-end walkthrough that checks cmd/kiosk-mcp passes
// against the mcp-go server: the checks did not change, only the
// protocol layer did.
func TestWalkthroughOnMCPGo(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	var auditLog bytes.Buffer
	h, tok, err := build(srv.URL, &auditLog)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out strings.Builder
	if err := mcpclient.Walkthrough(ctx, &out, srv.URL+"/mcp", tok); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	t.Log(out.String())

	// One audit line per tools/call, with an argument hash and never the
	// arguments themselves.
	outcomes := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(auditLog.String()), "\n") {
		var ev struct {
			Tool, Outcome, Tenant string
			ArgsHash              string `json:"args_sha256"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not JSON: %q", line)
		}
		if ev.Tenant != "seller-a" || ev.ArgsHash == "" || strings.Contains(line, "damaged") {
			t.Fatalf("bad audit line: %s", line)
		}
		outcomes[ev.Outcome]++
	}
	for _, want := range []string{"ok", "tool_error", "replayed", "rate_limited"} {
		if outcomes[want] == 0 {
			t.Errorf("no %q in audit log: %v", want, outcomes)
		}
	}
}

// A token without refunds:write neither sees nor calls the refund tools.
func TestScopeHidesAndBlocksTools(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	h, _, err := build(srv.URL, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	v := auth.Validator{Issuer: "https://auth.kiosk.example", Resource: srv.URL + "/mcp", Key: []byte("dev-only-key")}
	tok := v.Issue(auth.Claims{Issuer: v.Issuer, Subject: "u", Audience: []string{v.Resource},
		Scope: "orders:read", Tenant: "seller-a", ClientID: "ro", Expiry: time.Now().Add(time.Hour).Unix()})
	c := &mcpclient.Client{URL: srv.URL + "/mcp", Token: tok}
	ctx := context.Background()

	r, err := c.Do(ctx, "tools/list", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(mcpclient.ToolNames(r), ","); got != "lookup_order" {
		t.Fatalf("read-only token sees %q", got)
	}
	r, err = c.CallTool(ctx, "refund_order", map[string]any{"order_id": "K-1001", "reason": "damaged"}, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != 403 || !strings.Contains(r.Header.Get("WWW-Authenticate"), `scope="refunds:write"`) {
		t.Fatalf("want 403 insufficient_scope, got %d %q", r.Status, r.Header.Get("WWW-Authenticate"))
	}
}

// A token minted for another server is refused before mcp-go runs.
func TestWrongAudience(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	h, _, err := build(srv.URL, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	v := auth.Validator{Issuer: "https://auth.kiosk.example", Resource: "https://payments.kiosk.example/api", Key: []byte("dev-only-key")}
	tok := v.Issue(auth.Claims{Issuer: v.Issuer, Subject: "u", Audience: []string{v.Resource},
		Scope: "orders:read", Tenant: "seller-a", ClientID: "x", Expiry: time.Now().Add(time.Hour).Unix()})
	r, err := (&mcpclient.Client{URL: srv.URL + "/mcp", Token: tok}).Do(context.Background(), "tools/list", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != 401 || !strings.Contains(r.Header.Get("WWW-Authenticate"), "invalid_token") {
		t.Fatalf("want 401 invalid_token, got %d %q", r.Status, r.Header.Get("WWW-Authenticate"))
	}
}
