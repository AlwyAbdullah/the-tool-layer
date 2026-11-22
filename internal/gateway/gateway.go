// Package gateway is the policy core of an MCP gateway: it namespaces
// tools from several upstream servers, pins what each tool says about
// itself, quarantines tools whose descriptions changed or look like
// injection, and decides which agent may call which tool. Proxying the
// calls themselves is ordinary HTTP plumbing and is left out.
package gateway

import (
	"fmt"

	"github.com/AlwyAbdullah/the-tool-layer/internal/sanitize"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
)

// Upstream is one MCP server behind the gateway and the tools it
// listed.
type Upstream struct {
	Prefix string // short, unique, chosen by the gateway operator
	Tools  []toolspec.Tool
}

// Quarantined is a tool held back for a human to review.
type Quarantined struct {
	Tool   string
	Reason string
}

// Pins maps a namespaced tool name to the fingerprint a human approved.
type Pins map[string]string

// Namespace prefixes a tool name so two servers' "search" tools can't
// collide or shadow each other.
func Namespace(prefix string, t toolspec.Tool) (toolspec.Tool, error) {
	t.Name = prefix + "." + t.Name
	if len(t.Name) > 128 {
		return t, fmt.Errorf("namespaced name %q is too long", t.Name)
	}
	return t, nil
}

// Review builds the catalog agents will see. A tool is exposed only if
// it is pinned, unchanged since it was pinned, and its description
// scans clean.
func Review(pins Pins, ups []Upstream) (exposed []toolspec.Tool,
	held []Quarantined) {
	for _, u := range ups {
		for _, t := range u.Tools {
			nt, err := Namespace(u.Prefix, t)
			fs := sanitize.ScanDescription(t.Description)
			reason := ""
			switch {
			case err != nil:
				reason = err.Error()
			case len(fs) > 0:
				reason = fmt.Sprint("suspicious: ", sanitize.Rules(fs))
			case pins[nt.Name] == "":
				reason = "new tool, not yet approved"
			case pins[nt.Name] != toolspec.Fingerprint(t):
				reason = "changed since approval"
			default:
				exposed = append(exposed, nt)
				continue
			}
			held = append(held, Quarantined{nt.Name, reason})
		}
	}
	return exposed, held
}

// Approve pins the current version of a tool after a human reviewed it.
func Approve(pins Pins, prefix string, t toolspec.Tool) {
	pins[prefix+"."+t.Name] = toolspec.Fingerprint(t)
}

// Policy says which agent (OAuth client) may call which namespaced
// tools. Anything not listed is denied.
type Policy map[string][]string

// Permit reports whether client may call tool.
func (p Policy) Permit(client, tool string) bool {
	for _, t := range p[client] {
		if t == tool {
			return true
		}
	}
	return false
}
