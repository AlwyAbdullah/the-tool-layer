// Package idem makes side-effecting tool calls safe to repeat. Agents
// retry, clients re-send after a dropped stream, and models call the
// same tool twice. One intention, one key, one side effect.
package idem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrConflict   = errors.New("idempotency key reused with different arguments")
	ErrInProgress = errors.New("a call with this key is still running")
)

// Key scopes a caller-supplied or natural key to a tenant and a tool,
// so two tenants (or two tools) can never collide on the same key.
func Key(tenant, tool, key string) string {
	sum := sha256.Sum256([]byte(tenant + "\x00" + tool + "\x00" + key))
	return hex.EncodeToString(sum[:])
}

// Fingerprint hashes the canonical form of the arguments: key order and
// whitespace don't matter, values do.
func Fingerprint(args json.RawMessage) (string, error) {
	d := json.NewDecoder(bytes.NewReader(args))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return "", err
	}
	canon, err := json.Marshal(v) // Go sorts map keys
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

type entry struct {
	fp      string
	done    bool
	result  []byte
	expires time.Time
}

// Store remembers results per key. In production this is a table with a
// unique constraint on the key, written in the same transaction as the
// side effect where possible.
type Store struct {
	TTL time.Duration
	Now func() time.Time

	mu sync.Mutex
	m  map[string]*entry
}

// Do runs fn at most once per key and replays the stored result
// afterwards.
func (s *Store) Do(key, fp string,
	fn func() ([]byte, error)) ([]byte, bool, error) {
	if out, replayed, err := s.begin(key, fp); replayed || err != nil {
		return out, replayed, err
	}
	out, err := fn()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		delete(s.m, key) // a clean failure may be retried
		return nil, false, err
	}
	s.m[key] = &entry{fp: fp, done: true, result: out,
		expires: s.Now().Add(s.TTL)}
	return out, false, nil
}

func (s *Store) begin(key, fp string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]*entry{}
	}
	if e, ok := s.m[key]; ok && (!e.done || s.Now().Before(e.expires)) {
		switch {
		case e.fp != fp:
			return nil, false, ErrConflict
		case !e.done:
			return nil, false, ErrInProgress
		default:
			return e.result, true, nil
		}
	}
	s.m[key] = &entry{fp: fp}
	return nil, false, nil
}
