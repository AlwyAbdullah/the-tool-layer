package tenant

import (
	"context"
	"errors"
	"testing"
)

func seed() *Orders {
	o := &Orders{}
	o.Put(Order{ID: "K-1001", Tenant: "seller-a", Status: "delivered", AmountMinor: 6400, Currency: "USD"})
	o.Put(Order{ID: "K-2002", Tenant: "seller-b", Status: "shipped", AmountMinor: 1200, Currency: "USD"})
	return o
}

func TestTenantCannotSeeOtherTenant(t *testing.T) {
	o := seed()
	a := With(context.Background(), Principal{Tenant: "seller-a"})
	if _, err := o.Get(a, "K-1001"); err != nil {
		t.Fatalf("own order: %v", err)
	}
	if _, err := o.Get(a, "K-2002"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other tenant's order must look missing, got %v", err)
	}
}

func TestNoPrincipal(t *testing.T) {
	o := seed()
	if _, err := o.Get(context.Background(), "K-1001"); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("got %v", err)
	}
	if _, ok := From(With(context.Background(), Principal{})); ok {
		t.Fatal("principal without tenant must not count")
	}
}

func TestUpdateIsScoped(t *testing.T) {
	o := seed()
	b := With(context.Background(), Principal{Tenant: "seller-b"})
	_, err := o.Update(b, "K-1001", func(ord *Order) error { ord.Status = "refunded"; return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant update must fail, got %v", err)
	}
	a := With(context.Background(), Principal{Tenant: "seller-a"})
	got, err := o.Update(a, "K-1001", func(ord *Order) error { ord.Refunded = 6400; return nil })
	if err != nil || got.Refunded != 6400 {
		t.Fatalf("update: %+v %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := o.Update(a, "K-1001", func(*Order) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error must propagate, got %v", err)
	}
}
