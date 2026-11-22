package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func validator() Validator {
	return Validator{Issuer: "https://auth.kiosk.example", Resource: "https://mcp.kiosk.example/mcp",
		Key: []byte("test-key"), Leeway: 30 * time.Second, Now: func() time.Time { return now }}
}

func good() Claims {
	return Claims{Issuer: "https://auth.kiosk.example", Subject: "u-1", Audience: []string{"https://mcp.kiosk.example/mcp"},
		Scope: "orders:read refunds:write", Tenant: "seller-42", ClientID: "kiko", Expiry: now.Add(time.Hour).Unix()}
}

func TestValidate(t *testing.T) {
	v := validator()
	other := v
	other.Key = []byte("someone-else")
	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"good", v.Issue(good()), nil},
		{"no dot", "abc", ErrMalformed},
		{"forged", other.Issue(good()), ErrSignature},
		{"wrong issuer", v.Issue(func() Claims { c := good(); c.Issuer = "https://evil"; return c }()), ErrIssuer},
		{"token for another API", v.Issue(func() Claims { c := good(); c.Audience = []string{"https://api.payments.example"}; return c }()), ErrAudience},
		{"expired", v.Issue(func() Claims { c := good(); c.Expiry = now.Add(-time.Minute).Unix(); return c }()), ErrExpired},
		{"within leeway", v.Issue(func() Claims { c := good(); c.Expiry = now.Add(-10 * time.Second).Unix(); return c }()), nil},
		{"no tenant", v.Issue(func() Claims { c := good(); c.Tenant = ""; return c }()), ErrNoTenant},
	}
	for _, c := range cases {
		_, err := v.Validate(c.token)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestHasScope(t *testing.T) {
	implies := map[string][]string{"orders:write": {"orders:read"}}
	if !HasScope([]string{"orders:write"}, "orders:read", implies) {
		t.Error("broader scope should imply narrower")
	}
	if HasScope([]string{"orders:read"}, "orders:write", implies) {
		t.Error("narrower scope must not imply broader")
	}
	if HasScope(nil, "orders:read", implies) {
		t.Error("no scopes, no access")
	}
}

func TestBearerToken(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer abc.def")
	if tok, err := BearerToken(r); err != nil || tok != "abc.def" {
		t.Fatalf("got %q %v", tok, err)
	}
	r = httptest.NewRequest("POST", "/mcp?access_token=abc", nil)
	r.Header.Set("Authorization", "Bearer abc")
	if _, err := BearerToken(r); err == nil {
		t.Fatal("query-string token must be refused")
	}
	r = httptest.NewRequest("POST", "/mcp", nil)
	if _, err := BearerToken(r); err == nil {
		t.Fatal("missing header must fail")
	}
}

func TestChallenge(t *testing.T) {
	h := http.Header{}
	Challenge(h, "https://mcp.kiosk.example/.well-known/oauth-protected-resource", "insufficient_scope", "refunds:write")
	v := h.Get("WWW-Authenticate")
	for _, want := range []string{`Bearer resource_metadata="https://mcp.kiosk.example/`, `error="insufficient_scope"`, `scope="refunds:write"`} {
		if !strings.Contains(v, want) {
			t.Errorf("challenge %q missing %q", v, want)
		}
	}
}

func TestMetadataHandler(t *testing.T) {
	h := MetadataHandler(ResourceMetadata{Resource: "https://mcp.kiosk.example/mcp",
		AuthorizationServers: []string{"https://auth.kiosk.example"}, ScopesSupported: []string{"orders:read"}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/mcp", nil))
	var m ResourceMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Resource != "https://mcp.kiosk.example/mcp" || m.BearerMethods[0] != "header" {
		t.Fatalf("bad metadata %+v", m)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/.well-known/oauth-protected-resource", nil))
	if w.Code != 405 {
		t.Fatalf("POST should be 405, got %d", w.Code)
	}
}
