package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRecordWritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Record(Event{Time: time.Unix(0, 0).UTC(), TraceID: "t1", Tenant: "seller-a", Tool: "lookup_order", Outcome: OK})
	l.Record(Event{TraceID: "t2", Tenant: "seller-a", Tool: "refund_order", Outcome: Replayed})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: %d", len(lines))
	}
	var e Event
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil || e.Outcome != Replayed {
		t.Fatalf("%v %+v", err, e)
	}
}

func TestHashArgsHidesContent(t *testing.T) {
	h := HashArgs([]byte(`{"email":"buyer@example.com"}`))
	if len(h) != 16 || strings.Contains(h, "buyer") || h != HashArgs([]byte(`{"email":"buyer@example.com"}`)) {
		t.Fatalf("hash %q", h)
	}
}

func TestTraceID(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := TraceID(tp); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "garbage", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"} {
		got := TraceID(bad)
		if len(got) != 32 || got == "00000000000000000000000000000000" {
			t.Errorf("%q -> %q", bad, got)
		}
	}
	if TraceID("") == TraceID("") {
		t.Error("new IDs should be random")
	}
}
