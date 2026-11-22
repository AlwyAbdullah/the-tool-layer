// Package sanitize looks for signs of instructions hidden in text a
// model will read: tool descriptions (yours or a third party's) and
// tool results that carry other people's words. It is a signal for
// logging, review and blocking at publish time. It is not a guarantee:
// a determined attacker can phrase an instruction no pattern list will
// catch.
package sanitize

import (
	"regexp"
	"strings"
)

// Finding is one suspicious match.
type Finding struct {
	Rule    string
	Excerpt string
}

type rule struct {
	name string
	re   *regexp.Regexp
}

var textRules = []rule{
	{"override", regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b.{0,30}\b(previous|prior|above|earlier|all)\b.{0,20}\b(instructions?|rules?|prompts?)`)},
	{"secrecy", regexp.MustCompile(`(?i)\b(do not|don't|never)\s+(tell|mention|inform|show|reveal)\b.{0,20}\b(the )?(user|human|customer)`)},
	{"pseudo-tag", regexp.MustCompile(`(?i)<\s*/?\s*(important|system|instructions?|admin)\s*>`)},
	{"secrets", regexp.MustCompile(`(?i)(~/\.ssh|id_rsa|\.aws/credentials|\.env\b|mcp\.json|api[_-]?key|private key)`)},
	{"role-play", regexp.MustCompile(`(?i)\byou are now\b|\bnew instructions\b|\bsystem prompt\b`)},
}

var urlRule = rule{"url", regexp.MustCompile(`(?i)\bhttps?://\S+`)}

// invisible reports characters a human reviewer won't see but a model
// will read: zero-width characters, bidi controls and Unicode tag
// characters.
func invisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069,
		r == 0xFEFF, r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

// ScanResult checks text that came from outside (seller listings,
// reviews, emails, web pages) before it goes back to an agent inside a
// tool result.
func ScanResult(s string) []Finding {
	return scan(s, textRules)
}

// ScanDescription is stricter: a tool description should never contain
// a URL the model might be told to visit, so URLs are flagged too.
func ScanDescription(s string) []Finding {
	return scan(s, append(textRules[:len(textRules):len(textRules)], urlRule))
}

func scan(s string, rules []rule) []Finding {
	var out []Finding
	if strings.IndexFunc(s, invisible) >= 0 {
		out = append(out, Finding{Rule: "invisible", Excerpt: "hidden Unicode characters"})
	}
	visible := StripInvisible(s)
	for _, r := range rules {
		if m := r.re.FindString(visible); m != "" {
			out = append(out, Finding{Rule: r.name, Excerpt: excerpt(m)})
		}
	}
	return out
}

// StripInvisible removes the characters invisible reports.
func StripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, s)
}

func excerpt(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// Rules lists the rule names of findings, for logs and metrics.
func Rules(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}
