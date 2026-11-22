// Package tenant carries the caller's identity through a request and
// keeps each tenant's data out of every other tenant's reach. The
// tenant comes from the validated token, never from tool arguments the
// model wrote.
package tenant

import (
	"context"
	"errors"
	"sync"
)

// Principal is who is calling: the tenant (a seller account), the user,
// and the agent application acting for them.
type Principal struct {
	Tenant   string
	Subject  string
	ClientID string
	Scopes   []string
}

type ctxKey struct{}

// With stores the principal in the context. Only auth middleware calls
// it.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal, if any.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok && p.Tenant != ""
}

var (
	ErrNoPrincipal = errors.New("no authenticated principal")
	ErrNotFound    = errors.New("not found")
)

// Order is one marketplace order.
type Order struct {
	ID          string `json:"order_id"`
	Tenant      string `json:"-"`
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Refunded    int64  `json:"refunded_minor"`
}

// Orders is an in-memory, tenant-partitioned store. Every read goes
// through the principal in the context; there is no "get any order"
// method.
type Orders struct {
	mu sync.Mutex
	m  map[string]Order // key: tenant + "/" + order ID
}

// Put adds or replaces an order (seeding and tests).
func (o *Orders) Put(ord Order) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]Order{}
	}
	o.m[ord.Tenant+"/"+ord.ID] = ord
}

// Get returns the caller's order. Another tenant's order is reported as
// not found, so the caller can't even learn that it exists.
func (o *Orders) Get(ctx context.Context, id string) (Order, error) {
	p, ok := From(ctx)
	if !ok {
		return Order{}, ErrNoPrincipal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	ord, ok := o.m[p.Tenant+"/"+id]
	if !ok {
		return Order{}, ErrNotFound
	}
	return ord, nil
}

// Update applies fn to the caller's order under the store lock.
func (o *Orders) Update(ctx context.Context, id string, fn func(*Order) error) (Order, error) {
	p, ok := From(ctx)
	if !ok {
		return Order{}, ErrNoPrincipal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	key := p.Tenant + "/" + id
	ord, ok := o.m[key]
	if !ok {
		return Order{}, ErrNotFound
	}
	if err := fn(&ord); err != nil {
		return Order{}, err
	}
	o.m[key] = ord
	return ord, nil
}
