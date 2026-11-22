// Package ratelimit protects a tool server from bursts (token bucket
// per key), from slow drains (daily quota per tenant), and keeps a
// meter so every unit of work can be attributed to a tenant, an agent
// and a tool.
package ratelimit

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Limiter is a token bucket per key. Rate is tokens per second; Burst
// is the bucket size. Expensive tools cost more than one token.
type Limiter struct {
	Rate  float64
	Burst float64
	Now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// Allow spends cost tokens from key's bucket. When it refuses, it says
// how long until the bucket will hold enough tokens.
func (l *Limiter) Allow(key string,
	cost float64) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.Burst, last: now}
		l.buckets[key] = b
	}
	refill := now.Sub(b.last).Seconds() * l.Rate
	b.tokens = math.Min(l.Burst, b.tokens+refill)
	b.last = now
	if b.tokens >= cost {
		b.tokens -= cost
		return true, 0
	}
	wait := (cost - b.tokens) / l.Rate
	return false, time.Duration(math.Ceil(wait*1000)) * time.Millisecond
}

// Quota is a daily allowance of units per tenant, reset at UTC
// midnight.
type Quota struct {
	Limit func(tenant string) int64
	Now   func() time.Time

	mu   sync.Mutex
	used map[string]int64 // key: tenant + "|" + day
}

// Spend reserves units for tenant. It refuses without spending anything
// if the reservation would cross the limit.
func (q *Quota) Spend(tenant string,
	units int64) (remaining int64, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.used == nil {
		q.used = map[string]int64{}
	}
	key := tenant + "|" + q.Now().UTC().Format("2006-01-02")
	limit := q.Limit(tenant)
	if q.used[key]+units > limit {
		return limit - q.used[key], false
	}
	q.used[key] += units
	return limit - q.used[key], true
}

// Usage is one line of the cost report.
type Usage struct {
	Tenant, Client, Tool string
	Calls, Units         int64
}

// Meter attributes work to (tenant, client, tool).
type Meter struct {
	mu sync.Mutex
	m  map[[3]string]*Usage
}

// Add records one call costing units.
func (m *Meter) Add(tenant, client, tool string, units int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[[3]string]*Usage{}
	}
	k := [3]string{tenant, client, tool}
	u, ok := m.m[k]
	if !ok {
		u = &Usage{Tenant: tenant, Client: client, Tool: tool}
		m.m[k] = u
	}
	u.Calls++
	u.Units += units
}

// Report returns usage sorted by units, highest first.
func (m *Meter) Report() []Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Usage, 0, len(m.m))
	for _, u := range m.m {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Units != out[j].Units {
			return out[i].Units > out[j].Units
		}
		return out[i].Tenant+out[i].Client+out[i].Tool < out[j].Tenant+out[j].Client+out[j].Tool
	})
	return out
}
