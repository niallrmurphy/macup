// Package cache stores provider results between scans so a rescan minutes
// later does not repeat every network request. Entries are keyed by the
// caller, who includes whatever must invalidate them (installed versions),
// and expire after a TTL.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

type envelope struct {
	Key    string          `json:"key"`
	Stored time.Time       `json:"stored"`
	Value  json.RawMessage `json:"value"`
}

// Open returns a store under the user cache directory. A zero or negative
// TTL disables the store: Get always misses and Put does nothing.
func Open(ttl time.Duration) *Store {
	dir, err := os.UserCacheDir()
	if err != nil || ttl <= 0 {
		return &Store{ttl: 0, now: time.Now}
	}
	return &Store{dir: filepath.Join(dir, "macup", "results"), ttl: ttl, now: time.Now}
}

// OpenDir is Open with an explicit directory, for tests.
func OpenDir(dir string, ttl time.Duration) *Store {
	return &Store{dir: dir, ttl: ttl, now: time.Now}
}

func (s *Store) Enabled() bool { return s != nil && s.ttl > 0 && s.dir != "" }

func (s *Store) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:16])+".json")
}

// Get decodes a fresh entry into v and reports whether one was found.
func (s *Store) Get(key string, v any) bool {
	if !s.Enabled() {
		return false
	}
	raw, err := os.ReadFile(s.path(key))
	if err != nil {
		return false
	}
	var e envelope
	if json.Unmarshal(raw, &e) != nil || e.Key != key || s.now().Sub(e.Stored) > s.ttl {
		return false
	}
	return json.Unmarshal(e.Value, v) == nil
}

// Put stores v under key. Failures are silent: the cache is an optimisation.
func (s *Store) Put(key string, v any) {
	if !s.Enabled() {
		return
	}
	value, err := json.Marshal(v)
	if err != nil {
		return
	}
	raw, err := json.Marshal(envelope{Key: key, Stored: s.now(), Value: value})
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.path(key), raw, 0o644)
}

// Prune removes entries older than the TTL. Called opportunistically.
func (s *Store) Prune() {
	if !s.Enabled() {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && s.now().Sub(info.ModTime()) > s.ttl {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}
