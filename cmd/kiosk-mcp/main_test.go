package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/AlwyAbdullah/the-tool-layer/internal/mcpclient"
)

// TestEndToEnd starts the real demo server on a random localhost port and
// runs the same walkthrough as cmd/kiosk-client against it.
func TestEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	var audit bytes.Buffer
	h, tok, err := build(base, &audit)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	defer srv.Close()

	var out bytes.Buffer
	err = mcpclient.Walkthrough(context.Background(), &out, base+"/mcp", tok)
	t.Log(out.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	// Every call left one parseable audit line, and none contains a token.
	lines := strings.Split(strings.TrimSpace(audit.String()), "\n")
	if len(lines) < 5 {
		t.Fatalf("only %d audit lines", len(lines))
	}
	for _, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("audit line is not JSON: %s", l)
		}
		if strings.Contains(l, tok) {
			t.Fatal("token leaked into the audit log")
		}
	}

	// RFC 9728 metadata is served where the 401 challenge points.
	resp, err := http.Get(base + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var md map[string]any
	json.NewDecoder(resp.Body).Decode(&md)
	if md["resource"] != base+"/mcp" {
		t.Fatalf("metadata = %v", md)
	}
}
