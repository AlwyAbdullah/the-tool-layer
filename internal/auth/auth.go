// Package auth validates bearer tokens the way an MCP server acting as
// an OAuth 2.1 resource server must: signature, issuer, expiry, and
// above all audience. There is no real identity provider here; tokens
// are a compact HMAC-signed format so the checks are visible and
// testable. In production, validate your IdP's JWTs (or introspect
// opaque tokens) with the same rules.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Claims are the fields the server relies on.
type Claims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience []string `json:"aud"`
	Scope    string   `json:"scope"`
	Tenant   string   `json:"tenant"`
	ClientID string   `json:"client_id"`
	Expiry   int64    `json:"exp"`
}

// Scopes splits the space-separated scope claim.
func (c Claims) Scopes() []string { return strings.Fields(c.Scope) }

var (
	ErrMalformed = errors.New("malformed token")
	ErrSignature = errors.New("bad signature")
	ErrIssuer    = errors.New("unexpected issuer")
	ErrAudience  = errors.New("token was not issued for this server")
	ErrExpired   = errors.New("token expired")
	ErrNoTenant  = errors.New("token has no tenant")
)

// Validator checks tokens for one MCP server (one resource).
type Validator struct {
	Issuer   string // the authorization server we trust
	Resource string // this server's canonical URI, e.g. https://mcp.kiosk.example/mcp
	Key      []byte
	Leeway   time.Duration
	Now      func() time.Time
}

// Validate returns the claims only if every check passes.
func (v Validator) Validate(token string) (Claims, error) {
	var c Claims
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return c, ErrMalformed
	}
	if !hmac.Equal([]byte(sig), []byte(v.sign(body))) {
		return c, ErrSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, ErrMalformed
	}
	if c.Issuer != v.Issuer {
		return c, ErrIssuer
	}
	if !slices.Contains(c.Audience, v.Resource) {
		return c, ErrAudience // a token meant for another API
	}
	if v.now().After(time.Unix(c.Expiry, 0).Add(v.Leeway)) {
		return c, ErrExpired
	}
	if c.Tenant == "" {
		return c, ErrNoTenant
	}
	return c, nil
}

// Issue signs claims. It exists for tests and the local demo only.
func (v Validator) Issue(c Claims) string {
	raw, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + v.sign(body)
}

func (v Validator) sign(body string) string {
	m := hmac.New(sha256.New, v.Key)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (v Validator) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// HasScope reports whether granted covers need, following an explicit
// hierarchy (a broader scope implies narrower ones). No wildcards.
func HasScope(granted []string, need string, implies map[string][]string) bool {
	for _, g := range granted {
		if g == need || slices.Contains(implies[g], need) {
			return true
		}
	}
	return false
}

// BearerToken extracts the token from the Authorization header. Tokens
// in the query string are refused outright.
func BearerToken(r *http.Request) (string, error) {
	if r.URL.Query().Has("access_token") {
		return "", errors.New("tokens are not accepted in the query string")
	}
	h := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return "", errors.New("missing bearer token")
	}
	return strings.TrimSpace(tok), nil
}

// Challenge sets an RFC 6750 / RFC 9728 challenge for a 401 or 403, so
// the client can discover the authorization server and the scopes it
// needs.
func Challenge(h http.Header, metadataURL, errCode, scope string) {
	v := fmt.Sprintf("Bearer resource_metadata=%q", metadataURL)
	if errCode != "" {
		v += fmt.Sprintf(", error=%q", errCode)
	}
	if scope != "" {
		v += fmt.Sprintf(", scope=%q", scope)
	}
	h.Set("WWW-Authenticate", v)
}
