package ratelimit

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time        { return c.t }
func (c *clock) add(d time.Duration)   { c.t = c.t.Add(d) }
func newClock() *clock                 { return &clock{time.Date(2026, 11, 3, 23, 59, 0, 0, time.UTC)} }
func fixed(n int64) func(string) int64 { return func(string) int64 { return n } }

func TestBucketBurstThenRefill(t *testing.T) {
	c := newClock()
	l := &Limiter{Rate: 2, Burst: 5, Now: c.now}
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("seller-a", 1); !ok {
			t.Fatalf("call %d should pass within burst", i)
		}
	}
	ok, wait := l.Allow("seller-a", 1)
	if ok || wait != 500*time.Millisecond {
		t.Fatalf("6th call: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("seller-b", 1); !ok {
		t.Fatal("another tenant has its own bucket")
	}
	c.add(500 * time.Millisecond)
	if ok, _ := l.Allow("seller-a", 1); !ok {
		t.Fatal("bucket should have refilled one token")
	}
}

func TestExpensiveToolCostsMore(t *testing.T) {
	c := newClock()
	l := &Limiter{Rate: 1, Burst: 10, Now: c.now}
	if ok, _ := l.Allow("a", 8); !ok {
		t.Fatal("first expensive call fits")
	}
	ok, wait := l.Allow("a", 8)
	if ok || wait != 6*time.Second {
		t.Fatalf("ok=%v wait=%v", ok, wait)
	}
}

func TestQuotaResetsDaily(t *testing.T) {
	c := newClock()
	q := &Quota{Limit: fixed(10), Now: c.now}
	if rem, ok := q.Spend("a", 7); !ok || rem != 3 {
		t.Fatalf("rem=%d ok=%v", rem, ok)
	}
	if rem, ok := q.Spend("a", 4); ok || rem != 3 {
		t.Fatalf("over-limit spend must be refused without spending: rem=%d ok=%v", rem, ok)
	}
	c.add(2 * time.Minute) // past UTC midnight
	if _, ok := q.Spend("a", 4); !ok {
		t.Fatal("new day, new quota")
	}
}

func TestMeterReport(t *testing.T) {
	var m Meter
	m.Add("a", "kiko", "lookup_order", 1)
	m.Add("a", "kiko", "lookup_order", 1)
	m.Add("b", "acme-agent", "search_catalog", 5)
	r := m.Report()
	if len(r) != 2 || r[0].Tenant != "b" || r[0].Units != 5 || r[1].Calls != 2 {
		t.Fatalf("report %+v", r)
	}
}
