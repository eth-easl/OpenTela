package server

import (
	"strings"
	"sync"
	"time"
)

// affinityHeader is the single session header. A client sets one stable
// value per conversation (or the pi client sets it once per provider via
// models.json `headers`). Two tiers consume it:
//
//   - The billing/proxy tier (api.opentela.ai) maps the value to
//     langfuse.session.id so per-turn traces group into one Langfuse session
//     (correlation only; that proxy never persists conversation state).
//   - This mesh head maps the value to a sticky peer via AffinityStore, so a
//     multi-turn conversation reuses the same worker's warm KV cache.
//
// One header, one concept: "session affinity" = correlation + routing.
const affinityHeader = "X-Session-Affinity"

// affinityMax bounds the key length so an oversized or hostile value cannot
// bloat the routing map. Matches the api.opentela.ai session-id cap.
const affinityMax = 128

type affinityEntry struct {
	peer      string
	expiresAt time.Time
}

// AffinityStore maps a client-supplied affinity key to the peer that most
// recently served it, with a TTL. The mesh head consults it on every request
// to bias selection toward the pinned peer (warm KV cache across multi-turn
// conversations) and refreshes the pin after a successful forward.
//
// Best-effort and in-memory: the map is per-head and never replicated, so
// stickiness holds while requests for one affinity key land on the same head
// (the common case behind a stable ingress). A pinned peer that leaves the
// mesh, becomes unaffordable (drops out of X-Otela-Allowed-Peers), or is
// excluded by a retry is simply skipped — the head falls back to its normal
// load-balancing policy and the next successful forward re-pins.
type AffinityStore struct {
	mu  sync.RWMutex
	now func() time.Time
	m   map[string]affinityEntry
}

// NewAffinityStore returns an in-memory affinity map. now is injected for
// deterministic TTL tests; pass nil for time.Now.
func NewAffinityStore(now func() time.Time) *AffinityStore {
	if now == nil {
		now = time.Now
	}
	return &AffinityStore{now: now, m: make(map[string]affinityEntry)}
}

// Get returns the pinned peer for key when the pin has not expired. A miss
// (no pin or expired) is reported as ok=false; the entry is lazily evicted
// on expiry.
func (s *AffinityStore) Get(key string) (peer string, ok bool) {
	if key == "" {
		return "", false
	}
	s.mu.RLock()
	e, hit := s.m[key]
	s.mu.RUnlock()
	if !hit {
		return "", false
	}
	if s.now().After(e.expiresAt) {
		s.mu.Lock()
		// Re-check under the write lock: a concurrent Put may have
		// refreshed the pin between the RLock read and now.
		if e2, still := s.m[key]; still && s.now().After(e2.expiresAt) {
			delete(s.m, key)
		}
		s.mu.Unlock()
		return "", false
	}
	return e.peer, true
}

// Put records (or refreshes) the pin for key with the given TTL. A no-op for
// an empty key/peer or non-positive TTL.
func (s *AffinityStore) Put(key, peer string, ttl time.Duration) {
	if key == "" || peer == "" || ttl <= 0 {
		return
	}
	s.mu.Lock()
	s.m[key] = affinityEntry{peer: peer, expiresAt: s.now().Add(ttl)}
	s.mu.Unlock()
}

// Size returns the number of non-expired pins, evicting expired entries
// opportunistically. Mainly for diagnostics and tests.
func (s *AffinityStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.m {
		if now.After(e.expiresAt) {
			delete(s.m, k)
		}
	}
	return len(s.m)
}

// sanitizeAffinityKey trims surrounding whitespace, keeps only printable
// ASCII, and caps the length so a malicious or oversized value cannot bloat
// the routing map or carry control characters into a peer selection. The
// empty result means "no affinity" (no routing bias, no Langfuse
// correlation) — every consumer must treat "" as absent.
func sanitizeAffinityKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r >= 0x20 && r < 0x7f { // printable ASCII
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		return ""
	}
	if len(s) > affinityMax {
		s = s[:affinityMax]
	}
	return s
}

// selectAffinityPeer returns the pinned peer when it is still among the
// remaining candidates, or "" to defer to the normal selection policy. This
// is the per-attempt gate: a pinned peer excluded by a failed retry, or one
// that never made the affordable/trusted/ACL intersection, is skipped.
func selectAffinityPeer(remaining []string, pinned string) string {
	if pinned == "" {
		return ""
	}
	for _, p := range remaining {
		if p == pinned {
			return pinned
		}
	}
	return ""
}

// globalAffinity is the singleton store consulted by GlobalServiceForwardHandler.
var globalAffinity = NewAffinityStore(nil)
