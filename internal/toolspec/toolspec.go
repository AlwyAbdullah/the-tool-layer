// Package toolspec keeps tool definitions in one versioned registry:
// names that follow the MCP naming rules, required scopes, rate-limit
// cost, deprecation with a sunset date, and a fingerprint clients can
// pin.
package toolspec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Annotations are hints for clients (MCP ToolAnnotations). They
// describe intent; they enforce nothing, and clients must not trust
// them from servers they don't trust.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Bool returns a pointer, for annotation literals.
func Bool(b bool) *bool { return &b }

// Deprecation announces that a tool is going away.
type Deprecation struct {
	Since       time.Time
	Sunset      time.Time // after this, calls fail and name Replacement
	Replacement string
}

// Spec is the server-side definition of one tool.
type Spec struct {
	Name         string
	Title        string
	Description  string
	Version      int
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Annotations  Annotations
	Scope        string // required OAuth scope
	Cost         float64
	Deprecated   *Deprecation
}

// Tool is the wire shape returned by tools/list.
type Tool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  *Annotations    `json:"annotations,omitempty"`
	Meta         map[string]any  `json:"_meta,omitempty"`
}

var validName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

var (
	ErrUnknown = errors.New("unknown tool")
	ErrRetired = errors.New("tool retired")
)

// Registry holds the server's tools.
type Registry struct {
	specs map[string]Spec
}

// Add registers a spec after checking the name and the schema root.
func (r *Registry) Add(s Spec) error {
	if !validName.MatchString(s.Name) {
		return fmt.Errorf("tool name %q: use 1-128 of A-Z a-z 0-9 _ - .", s.Name)
	}
	var root struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(s.InputSchema, &root) != nil || root.Type != "object" {
		return fmt.Errorf("tool %q: inputSchema must be a JSON Schema object", s.Name)
	}
	if _, dup := r.specs[s.Name]; dup {
		return fmt.Errorf("tool %q registered twice", s.Name)
	}
	if r.specs == nil {
		r.specs = map[string]Spec{}
	}
	r.specs[s.Name] = s
	return nil
}

// List returns the tools the caller may use, in a deterministic order
// so clients (and model prompt caches) see the same list every time.
func (r *Registry) List(allowed func(scope string) bool, now time.Time) []Tool {
	out := []Tool{}
	for _, s := range r.specs {
		if !allowed(s.Scope) || (s.Deprecated != nil && now.After(s.Deprecated.Sunset)) {
			continue
		}
		out = append(out, s.wire())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resolve finds the spec for a call. A deprecated tool still works and
// returns a warning; a retired one returns ErrRetired.
func (r *Registry) Resolve(name string,
	now time.Time) (Spec, string, error) {
	s, ok := r.specs[name]
	if !ok {
		return Spec{}, "", ErrUnknown
	}
	d := s.Deprecated
	if d == nil || now.Before(d.Since) {
		return s, "", nil
	}
	msg := fmt.Sprintf("%s is deprecated, stops on %s; use %s",
		name, d.Sunset.Format("2006-01-02"), d.Replacement)
	if now.After(d.Sunset) {
		return s, msg, ErrRetired
	}
	return s, msg, nil
}

func (s Spec) wire() Tool {
	t := Tool{Name: s.Name, Title: s.Title, Description: s.Description,
		InputSchema: s.InputSchema, OutputSchema: s.OutputSchema,
		Meta: map[string]any{"co.kiosk/version": s.Version}}
	if s.Annotations != (Annotations{}) {
		a := s.Annotations
		t.Annotations = &a
	}
	if d := s.Deprecated; d != nil {
		t.Description = fmt.Sprintf("DEPRECATED: use %s. %s", d.Replacement, s.Description)
		t.Meta["co.kiosk/sunset"] = d.Sunset.Format("2006-01-02")
	}
	return t
}

// Fingerprint hashes everything the model reads about a tool. Clients
// and gateways pin it; a change between two listings is a reason to
// stop and look (a "rug pull" starts as an innocent-looking description
// change).
func Fingerprint(t Tool) string {
	b, _ := json.Marshal(struct {
		N, D string
		I    json.RawMessage
		A    *Annotations
	}{t.Name, t.Description, t.InputSchema, t.Annotations})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
