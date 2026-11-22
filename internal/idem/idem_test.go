package idem

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func store() (*Store, *time.Time) {
	t := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	return &Store{TTL: 24 * time.Hour, Now: func() time.Time { return t }}, &t
}

func TestFingerprintIgnoresKeyOrder(t *testing.T) {
	a, _ := Fingerprint(json.RawMessage(`{"order_id":"K-1","reason":"damaged"}`))
	b, _ := Fingerprint(json.RawMessage(`{ "reason":"damaged", "order_id":"K-1" }`))
	c, _ := Fingerprint(json.RawMessage(`{"order_id":"K-2","reason":"damaged"}`))
	if a != b || a == c {
		t.Fatalf("a=%s b=%s c=%s", a, b, c)
	}
	if _, err := Fingerprint(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestKeyIsScoped(t *testing.T) {
	if Key("a", "refund_order", "K-1") == Key("b", "refund_order", "K-1") {
		t.Fatal("tenants must not share keys")
	}
	if Key("a", "refund_order", "K-1") == Key("a", "cancel_order", "K-1") {
		t.Fatal("tools must not share keys")
	}
}

func TestDoRunsOnceAndReplays(t *testing.T) {
	s, _ := store()
	var runs int
	fn := func() ([]byte, error) { runs++; return []byte(`{"refund_id":"R-1"}`), nil }
	out1, rep1, err1 := s.Do("k", "fp", fn)
	out2, rep2, err2 := s.Do("k", "fp", fn)
	if err1 != nil || err2 != nil || rep1 || !rep2 || string(out1) != string(out2) || runs != 1 {
		t.Fatalf("runs=%d rep1=%v rep2=%v", runs, rep1, rep2)
	}
}

func TestConflict(t *testing.T) {
	s, _ := store()
	s.Do("k", "fp1", func() ([]byte, error) { return []byte("x"), nil })
	if _, _, err := s.Do("k", "fp2", func() ([]byte, error) { return nil, nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestFailureCanBeRetried(t *testing.T) {
	s, _ := store()
	boom := errors.New("downstream refused")
	if _, _, err := s.Do("k", "fp", func() ([]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	out, rep, err := s.Do("k", "fp", func() ([]byte, error) { return []byte("ok"), nil })
	if err != nil || rep || string(out) != "ok" {
		t.Fatalf("retry after failure: %s %v %v", out, rep, err)
	}
}

func TestExpiry(t *testing.T) {
	s, now := store()
	var runs int
	fn := func() ([]byte, error) { runs++; return []byte("x"), nil }
	s.Do("k", "fp", fn)
	*now = now.Add(25 * time.Hour)
	s.Do("k", "fp", fn)
	if runs != 2 {
		t.Fatalf("expired key should run again, runs=%d", runs)
	}
}

func TestConcurrentSameKey(t *testing.T) {
	s, _ := store()
	var runs atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do("k", "fp", func() ([]byte, error) { runs.Add(1); close(started); <-release; return []byte("x"), nil })
	<-started
	if _, _, err := s.Do("k", "fp", func() ([]byte, error) { runs.Add(1); return nil, nil }); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second concurrent call: %v", err)
	}
	close(release)
	if runs.Load() != 1 {
		t.Fatalf("runs=%d", runs.Load())
	}
}
