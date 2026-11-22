// Package audit writes one structured record per tool call: who
// (tenant, user, agent), what (tool, argument hash), how it ended, how
// long it took, and the trace ID that ties it to the agent's own logs.
package audit

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

// Outcomes, kept to a small fixed set so dashboards can group by them.
const (
	OK          = "ok"
	ToolError   = "tool_error"
	Denied      = "denied"
	RateLimited = "rate_limited"
	Replayed    = "replayed"
	Flagged     = "flagged"
)

// Event is one audit record. Arguments are hashed, not stored: the
// audit log must not become a second copy of customer data.
type Event struct {
	Time       time.Time `json:"time"`
	TraceID    string    `json:"trace_id"`
	Tenant     string    `json:"tenant"`
	Subject    string    `json:"subject"`
	Client     string    `json:"client"`
	Tool       string    `json:"tool"`
	ToolVer    int       `json:"tool_version,omitempty"`
	ArgsHash   string    `json:"args_sha256,omitempty"`
	Outcome    string    `json:"outcome"`
	Detail     string    `json:"detail,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Findings   []string  `json:"findings,omitempty"`
}

// Logger appends JSON lines to w.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// New returns a logger writing to w.
func New(w io.Writer) *Logger { return &Logger{w: w} }

// Record writes one event.
func (l *Logger) Record(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(b, '\n'))
	return err
}

// HashArgs returns a short, stable hash of the raw arguments.
func HashArgs(args []byte) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:8])
}

// TraceID takes the trace ID from a W3C traceparent value ("00-<32
// hex>-<16 hex>-<2 hex>"), which MCP clients may send in _meta. If it
// is missing or invalid, a new random ID is returned.
func TraceID(traceparent string) string {
	parts := strings.Split(traceparent, "-")
	if len(parts) == 4 && len(parts[1]) == 32 && isHex(parts[1]) &&
		strings.Trim(parts[1], "0") != "" {
		return parts[1]
	}
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}
