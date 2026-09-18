package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// sanitizeAffinityKey
// ---------------------------------------------------------------------------

func TestSanitizeAffinityKey_AcceptsPrintable(t *testing.T) {
	assert.Equal(t, "user-42", sanitizeAffinityKey("user-42"))
}

func TestSanitizeAffinityKey_TrimsSurroundingWhitespace(t *testing.T) {
	assert.Equal(t, "sess", sanitizeAffinityKey("   sess\t\n"))
}

func TestSanitizeAffinityKey_RejectsNonPrintable(t *testing.T) {
	// The newline is not printable ASCII and is dropped; the remaining
	// printable characters survive as a (harmless, map-only) key. The key is
	// never written to a header by the mesh head, so this is hygiene, not
	// an injection barrier.
	assert.Equal(t, "sessX-Evil: 1", sanitizeAffinityKey("sess\nX-Evil: 1"))
	// A value composed entirely of control characters collapses to "".
	assert.Equal(t, "", sanitizeAffinityKey("\n\r\t"))
}

func TestSanitizeAffinityKey_StripsNonAsciiKeepsRest(t *testing.T) {
	// Unicode is dropped; ASCII survives.
	assert.Equal(t, "abc", sanitizeAffinityKey("a✓b✗c"))
}

func TestSanitizeAffinityKey_EmptyIsAbsent(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\t"} {
		assert.Equal(t, "", sanitizeAffinityKey(in))
	}
}

func TestSanitizeAffinityKey_CapsLength(t *testing.T) {
	long := make([]byte, affinityMax+20)
	for i := range long {
		long[i] = 'a'
	}
	got := sanitizeAffinityKey(string(long))
	assert.Equal(t, affinityMax, len(got))
}

// ---------------------------------------------------------------------------
// AffinityStore — put / get / overwrite
// ---------------------------------------------------------------------------

func TestAffinityStore_EmptyKeyIsNoop(t *testing.T) {
	s := NewAffinityStore(nil)
	s.Put("", "peer-A", time.Minute)
	if p, ok := s.Get(""); ok || p != "" {
		t.Fatalf("empty key should be absent, got %q ok=%v", p, ok)
	}
	assert.Equal(t, 0, s.Size())
}

func TestAffinityStore_EmptyPeerIsNoop(t *testing.T) {
	s := NewAffinityStore(nil)
	s.Put("k", "", time.Minute)
	if _, ok := s.Get("k"); ok {
		t.Fatalf("empty peer should not be recorded")
	}
}

func TestAffinityStore_PutGetRoundTrip(t *testing.T) {
	s := NewAffinityStore(nil)
	s.Put("conv-1", "peer-A", time.Minute)
	got, ok := s.Get("conv-1")
	assert.True(t, ok)
	assert.Equal(t, "peer-A", got)
	assert.Equal(t, 1, s.Size())
}

func TestAffinityStore_OverwriteRefreshes(t *testing.T) {
	s := NewAffinityStore(nil)
	s.Put("conv-1", "peer-A", time.Minute)
	s.Put("conv-1", "peer-B", time.Minute)
	got, ok := s.Get("conv-1")
	assert.True(t, ok)
	assert.Equal(t, "peer-B", got)
	assert.Equal(t, 1, s.Size())
}

func TestAffinityStore_MissingKey(t *testing.T) {
	s := NewAffinityStore(nil)
	if _, ok := s.Get("never"); ok {
		t.Fatalf("unseen key should be a miss")
	}
}

// ---------------------------------------------------------------------------
// AffinityStore — TTL expiry (injected clock)
// ---------------------------------------------------------------------------

func TestAffinityStore_ExpiresAfterTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewAffinityStore(func() time.Time { return now })

	s.Put("conv-1", "peer-A", 10*time.Minute)

	if _, ok := s.Get("conv-1"); !ok {
		t.Fatalf("pin should be present before TTL")
	}

	// Advance past the TTL: the pin expires and is lazily evicted.
	now = now.Add(11 * time.Minute)
	if p, ok := s.Get("conv-1"); ok {
		t.Fatalf("pin should be expired, got %q", p)
	}
	assert.Equal(t, 0, s.Size(), "expired entry should be evicted")
}

func TestAffinityStore_PutExtendsTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewAffinityStore(func() time.Time { return now })

	s.Put("conv-1", "peer-A", 10*time.Minute)
	now = now.Add(9 * time.Minute) // just before expiry
	s.Put("conv-1", "peer-A", 10*time.Minute)
	now = now.Add(9 * time.Minute) // would be past the ORIGINAL ttl

	if p, ok := s.Get("conv-1"); !ok || p != "peer-A" {
		t.Fatalf("refreshed pin should still be present, got %q ok=%v", p, ok)
	}
}

// ---------------------------------------------------------------------------
// selectAffinityPeer — the per-attempt selection gate
// ---------------------------------------------------------------------------

func TestSelectAffinityPeer_PinnedPresent(t *testing.T) {
	remaining := []string{"peer-B", "peer-A", "peer-C"}
	assert.Equal(t, "peer-A", selectAffinityPeer(remaining, "peer-A"))
}

func TestSelectAffinityPeer_PinnedAbsent(t *testing.T) {
	// Pinned peer was excluded by a failed retry or left the mesh —
	// defer to the normal policy (return "").
	remaining := []string{"peer-B", "peer-C"}
	assert.Equal(t, "", selectAffinityPeer(remaining, "peer-A"))
}

func TestSelectAffinityPeer_NoPin(t *testing.T) {
	remaining := []string{"peer-B", "peer-C"}
	assert.Equal(t, "", selectAffinityPeer(remaining, ""))
}

func TestSelectAffinityPeer_EmptyRemaining(t *testing.T) {
	assert.Equal(t, "", selectAffinityPeer(nil, "peer-A"))
}
