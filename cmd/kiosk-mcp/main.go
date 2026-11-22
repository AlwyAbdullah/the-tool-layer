// Command kiosk-mcp runs the example tool server on localhost with
// seeded data and prints a development token. It is a demo, not a
// deployment.
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
	"github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
	"github.com/AlwyAbdullah/the-tool-layer/internal/ratelimit"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (keep it on localhost)")
	flag.Parse()
	base := "http://" + *addr
	mux, tok, err := build(base, os.Stderr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MCP endpoint: %s/mcp\nDev token (seller-a, 8h):\n%s\n", base, tok)
	fmt.Printf("\nTry it from another terminal:\n  go run ./cmd/kiosk-client -url %s/mcp -token <token>\n", base)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// build wires the demo server for base (e.g. "http://127.0.0.1:8080") and
// returns the handler and a development token for seller-a.
func build(base string, auditOut io.Writer) (http.Handler, string, error) {
	resource := base + "/mcp"

	orders := &tenant.Orders{}
	orders.Put(tenant.Order{ID: "K-1001", Tenant: "seller-a", Status: "delivered", AmountMinor: 6400, Currency: "USD"})
	orders.Put(tenant.Order{ID: "K-2001", Tenant: "seller-b", Status: "shipped", AmountMinor: 1200, Currency: "USD"})
	svc := &kiosk.Service{Orders: orders, AutoRefundLimit: 20000, Catalog: []kiosk.Item{
		{SKU: "J-1", Title: "Denim jacket", Description: "Washed denim, unisex.", PriceMinor: 6400, Currency: "USD"},
	}}
	reg := &toolspec.Registry{}
	handlers, err := svc.Register(reg, time.Now().AddDate(0, -1, 0))
	if err != nil {
		return nil, "", err
	}
	v := auth.Validator{Issuer: "https://auth.kiosk.example", Resource: resource, Key: []byte("dev-only-key"), Leeway: 30 * time.Second}
	srv := &mcp.Server{Name: "kiosk-seller-tools", Version: "1.4.0", Registry: reg, Handlers: handlers, Auth: v,
		Instructions: "Tools for one Kiosk & Co seller account: orders, refunds, catalog.",
		MetadataURL:  base + "/.well-known/oauth-protected-resource/mcp",
		Implies:      map[string][]string{"refunds:write": {"orders:read"}},
		Limiter:      &ratelimit.Limiter{Rate: 2, Burst: 20, Now: time.Now},
		Quota:        &ratelimit.Quota{Limit: func(string) int64 { return 5000 }, Now: time.Now},
		Meter:        &ratelimit.Meter{},
		Idem:         &idem.Store{TTL: 24 * time.Hour, Now: time.Now},
		Audit:        audit.New(auditOut)}

	mux := http.NewServeMux()
	mux.Handle("/mcp", srv)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", auth.MetadataHandler(auth.ResourceMetadata{
		Resource: resource, AuthorizationServers: []string{v.Issuer}, ScopesSupported: []string{"orders:read"}}))

	tok := v.Issue(auth.Claims{Issuer: v.Issuer, Subject: "demo-user", Audience: []string{resource},
		Scope: "orders:read refunds:write catalog:read", Tenant: "seller-a", ClientID: "demo",
		Expiry: time.Now().Add(8 * time.Hour).Unix()})
	return mux, tok, nil
}
