package kiosk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
	"github.com/AlwyAbdullah/the-tool-layer/internal/tenant"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
)

func svc() (*Service, context.Context) {
	o := &tenant.Orders{}
	o.Put(tenant.Order{ID: "K-1001", Tenant: "seller-a", Status: "delivered", AmountMinor: 6400, Currency: "USD"})
	s := &Service{Orders: o, AutoRefundLimit: 5000, Catalog: []Item{
		{SKU: "1", Title: "Denim jacket"}, {SKU: "2", Title: "Rain jacket"}, {SKU: "3", Title: "Wool hat"},
		{SKU: "4", Title: "Linen jacket​", Description: "Light.\U000E0041"},
	}}
	return s, tenant.With(context.Background(), tenant.Principal{Tenant: "seller-a"})
}

func run(t *testing.T, h mcp.Handler, ctx context.Context, args string) mcp.ToolResult {
	t.Helper()
	r, err := h(ctx, mcp.Call{Args: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPartialThenRest(t *testing.T) {
	s, ctx := svc()
	r := run(t, s.IssueRefund, ctx, `{"order_id":"K-1001","amount_minor":2000}`)
	if r.IsError || !strings.Contains(r.Text, "Refunded 2000") {
		t.Fatalf("%+v", r)
	}
	r = run(t, s.RefundOrder, ctx, `{"order_id":"K-1001","reason":"damaged"}`)
	if r.IsError || !strings.Contains(r.Text, "Refunded 4400") {
		t.Fatalf("rest of the order: %+v", r)
	}
	r = run(t, s.RefundOrder, ctx, `{"order_id":"K-1001","reason":"damaged"}`)
	if r.IsError || !strings.Contains(r.Text, "Nothing left") {
		t.Fatalf("second ask must be harmless: %+v", r)
	}
}

func TestAmountAboveLimitNeedsHuman(t *testing.T) {
	s, ctx := svc()
	r := run(t, s.RefundOrder, ctx, `{"order_id":"K-1001","reason":"damaged"}`)
	if !r.IsError || !strings.Contains(r.Text, "needs a human") {
		t.Fatalf("%+v", r)
	}
	ord, _ := s.Orders.Get(ctx, "K-1001")
	if ord.Refunded != 0 {
		t.Fatal("no money may move when a human is needed")
	}
}

func TestStrictArguments(t *testing.T) {
	s, ctx := svc()
	for _, args := range []string{`{"order_id":"K-1001","reason":"because"}`, `{"order_id":"K-1001","reason":"damaged","amount_minor":1}`, `[]`} {
		if r := run(t, s.RefundOrder, ctx, args); !r.IsError {
			t.Errorf("%s should be rejected", args)
		}
	}
	if r := run(t, s.IssueRefund, ctx, `{"order_id":"K-1001","amount_minor":-5}`); !r.IsError {
		t.Error("negative amount")
	}
}

func TestSearchCatalog(t *testing.T) {
	s, ctx := svc()
	r := run(t, s.SearchCatalog, ctx, `{"query":"JACKET","limit":2}`)
	items := r.Structured.(map[string]any)["items"].([]Item)
	if len(items) != 2 {
		t.Fatalf("limit not applied: %d", len(items))
	}
	r = run(t, s.SearchCatalog, ctx, `{"query":"linen"}`)
	it := r.Structured.(map[string]any)["items"].([]Item)[0]
	if it.Title != "Linen jacket" || it.Description != "Light." {
		t.Fatalf("invisible characters must be stripped: %q %q", it.Title, it.Description)
	}
}

func TestSpecsRegisterAndScanClean(t *testing.T) {
	s, _ := svc()
	r := &toolspec.Registry{}
	hs, err := s.Register(r, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(hs) != 4 {
		t.Fatalf("%v %d", err, len(hs))
	}
	for _, sp := range Specs(time.Now()) {
		var v any
		if err := json.Unmarshal(sp.InputSchema, &v); err != nil {
			t.Errorf("%s: schema is not valid JSON: %v", sp.Name, err)
		}
	}
}
