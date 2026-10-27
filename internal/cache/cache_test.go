package cache

import (
	"testing"
	"time"
)

func TestStoreRoundTripAndExpiry(t *testing.T) {
	s := OpenDir(t.TempDir(), time.Hour)
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	type v struct{ N int }
	if s.Get("k", &v{}) {
		t.Fatal("unexpected hit")
	}
	s.Put("k", v{N: 7})
	var got v
	if !s.Get("k", &got) || got.N != 7 {
		t.Fatalf("%+v", got)
	}
	if s.Get("other", &got) {
		t.Fatal("key must match")
	}
	s.now = func() time.Time { return base.Add(2 * time.Hour) }
	if s.Get("k", &got) {
		t.Fatal("expired entry served")
	}
	off := Open(0)
	off.Put("k", v{N: 1})
	if off.Get("k", &got) {
		t.Fatal("disabled store served")
	}
}
