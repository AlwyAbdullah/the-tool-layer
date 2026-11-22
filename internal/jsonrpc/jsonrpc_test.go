package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
	}{
		{"valid", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 0},
		{"string id", `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`, 0},
		{"notification", `{"jsonrpc":"2.0","method":"notifications/x"}`, 0},
		{"broken json", `{"jsonrpc":`, ParseError},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"x"}`, InvalidRequest},
		{"no method", `{"jsonrpc":"2.0","id":1}`, InvalidRequest},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"x"}`, InvalidRequest},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"x"}`, InvalidRequest},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.body))
		got := 0
		if err != nil {
			got = err.Code
		}
		if got != c.code {
			t.Errorf("%s: code %d, want %d", c.name, got, c.code)
		}
	}
}

func TestNotification(t *testing.T) {
	r, _ := Parse([]byte(`{"jsonrpc":"2.0","method":"notifications/x"}`))
	if !r.IsNotification() {
		t.Fatal("expected notification")
	}
}

func TestFailUsesNullID(t *testing.T) {
	b, _ := json.Marshal(Fail(nil, ParseError, "parse error", nil))
	if !strings.Contains(string(b), `"id":null`) {
		t.Fatalf("missing null id: %s", b)
	}
	b, _ = json.Marshal(Result(json.RawMessage(`7`), map[string]int{"a": 1}))
	if !strings.Contains(string(b), `"id":7`) || strings.Contains(string(b), "error") {
		t.Fatalf("bad result: %s", b)
	}
}
