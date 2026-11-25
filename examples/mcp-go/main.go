// Command mcp-go runs the book's Kiosk & Co tool server on
// github.com/mark3labs/mcp-go instead of the hand-written server in
// internal/mcp. mcp-go speaks the protocol; the checks from the book
// (auth, tenants, rate limits, idempotency, versioning, audit) are the
// same packages, wired in through net/http middleware, an mcp-go tool
// filter and an mcp-go tool middleware. See checks.go.
//
// Run it from this directory with: go run .
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/audit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/auth"
	"github.com/AlwyAbdullah/the-tool-layer/internal/idem"
	"github.com/AlwyAbdullah/the-tool-layer/internal/kiosk"
	"github.com/AlwyAbdullah/the-tool-layer/internal/ratelimit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (keep it on localhost)")
	flag.Parse()
	base := "http://" + *addr
	h, tok, err := build(base, os.Stderr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MCP endpoint (mcp-go): %s/mcp\nDev token (seller-a, 8h):\n%s\n", base, tok)
	fmt.Printf("\nTry it from the repository root:\n  go run ./cmd/kiosk-client -url %s/mcp -token <token>\n", base)
	log.Fatal(http.ListenAndServe(*addr, h))
}

// build wires the same demo as cmd/kiosk-mcp, on mcp-go.
func build(base string, auditOut io.Writer) (http.Handler, string, error) {
	resource := base + "/mcp"

	orders := &tenant.Orders{}
	orders.Put(tenant.Order{ID: "K-1001", Tenant: "seller-a", Status: "delivered", AmountMinor: 6400, Currency: "USD"})
	orders.Put(tenant.Order{ID: "K-2001", Tenant: "seller-b", Status: "shipped", AmountMinor: 1200, Currency: "USD"})
	svc := &kiosk.Service{Orders: orders, AutoRefundLimit: 20000, Catalog: []kiosk.Item{
		{SKU: "J-1", Title: "Denim jacket", Description: "Washed denim, unisex.", PriceMinor: 6400, Currency: "USD"},
	}}
	since := time.Now().AddDate(0, -1, 0)
	reg := &toolspec.Registry{}
	handlers, err := svc.Register(reg, since)
	if err != nil {
		return nil, "", err
	}

	v := auth.Validator{Issuer: "https://auth.kiosk.example", Resource: resource, Key: []byte("dev-only-key"), Leeway: 30 * time.Second}
	k := &checks{Auth: v, Registry: reg,
		MetadataURL: base + "/.well-known/oauth-protected-resource/mcp",
		Implies:     map[string][]string{"refunds:write": {"orders:read"}},
		Limiter:     &ratelimit.Limiter{Rate: 2, Burst: 20, Now: time.Now},
		Quota:       &ratelimit.Quota{Limit: func(string) int64 { return 5000 }, Now: time.Now},
		Meter:       &ratelimit.Meter{},
		Idem:        &idem.Store{TTL: 24 * time.Hour, Now: time.Now},
		Audit:       audit.New(auditOut)}

	s := server.NewMCPServer("kiosk-seller-tools", "1.4.0",
		server.WithInstructions("Tools for one Kiosk & Co seller account: orders, refunds, catalog."),
		server.WithToolCapabilities(false),
		server.WithToolFilter(k.visible),
		server.WithToolHandlerMiddleware(k.guard),
		server.WithMethodCacheHints(mcp.MethodToolsList, 60_000, mcp.CacheScopePrivate), // the list depends on the token
	)
	for _, spec := range kiosk.Specs(since) {
		s.AddTool(toolFor(spec), adapt(handlers[spec.Name]))
	}
	mcpHTTP := server.NewStreamableHTTPServer(s,
		server.WithEndpointPath("/mcp"),
		server.WithStateLess(true),
		server.WithHTTPContextFunc(carry))

	mux := http.NewServeMux()
	mux.Handle("/mcp", k.gate(mcpHTTP))
	mux.Handle("/.well-known/oauth-protected-resource/mcp", server.NewProtectedResourceMetadataHandler(
		server.ProtectedResourceMetadataConfig{Resource: resource, AuthorizationServers: []string{v.Issuer},
			ScopesSupported: []string{"orders:read", "refunds:write", "catalog:read"}}))

	tok := v.Issue(auth.Claims{Issuer: v.Issuer, Subject: "demo-user", Audience: []string{resource},
		Scope: "orders:read refunds:write catalog:read", Tenant: "seller-a", ClientID: "demo",
		Expiry: time.Now().Add(8 * time.Hour).Unix()})
	return mux, tok, nil
}
