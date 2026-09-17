package protocol

import (
	"encoding/json"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestUpdateNodeTableHook_SelfTrust(t *testing.T) {
	// Save and restore state.
	oldMyID := MyID
	oldVal := viper.GetBool("security.require_signed_binary")
	defer func() {
		MyID = oldMyID
		viper.Set("security.require_signed_binary", oldVal)
	}()

	_ = GetAllPeers()
	viper.Set("security.require_signed_binary", true)

	// An unsigned remote peer should be rejected.
	remote := Peer{ID: "remote-peer", PublicAddress: "1.2.3.4"}
	b, _ := json.Marshal(remote)
	UpdateNodeTableHook(ds.NewKey("remote-peer"), b)
	_, err := GetPeerFromTable("remote-peer")
	if err == nil {
		t.Fatal("expected unsigned remote peer to be rejected")
	}

	// The local node (matching MyID) should always be accepted, even unsigned.
	MyID = "self-node"
	self := Peer{ID: "self-node", PublicAddress: "5.6.7.8"}
	b, _ = json.Marshal(self)
	UpdateNodeTableHook(ds.NewKey("self-node"), b)
	got, err := GetPeerFromTable("self-node")
	if err != nil {
		t.Fatalf("expected self to be accepted even without signed build, got: %v", err)
	}
	if got.PublicAddress != "5.6.7.8" {
		t.Fatalf("unexpected peer: %+v", got)
	}
}

func TestUpdateNodeTableHookAndGetPeer(t *testing.T) {
	_ = GetAllPeers()
	p := Peer{ID: "peer1", PublicAddress: "1.2.3.4"}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer1"), b)

	got, err := GetPeerFromTable("peer1")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.PublicAddress != "1.2.3.4" {
		t.Fatalf("unexpected peer: %+v", got)
	}
}

func TestDeleteNodeTableHook(t *testing.T) {
	table := GetAllPeers()
	p := Peer{ID: "peer2", PublicAddress: "5.6.7.8"}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer2"), b)
	DeleteNodeTableHook(ds.NewKey("peer2"))
	if _, ok := (*table)["/peer2"]; ok {
		t.Fatalf("expected peer2 deleted")
	}
}
func TestNodeLeave(t *testing.T) {
	// 1. Setup initial state
	p := Peer{ID: "peer-leaving", PublicAddress: "10.0.0.1", Status: CONNECTED, Connected: true}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-leaving"), b)

	// Verify it's in the table
	got, err := GetPeerFromTable("peer-leaving")
	if err != nil {
		t.Fatalf("expected peer to be in table")
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected peer status to be connected, got %s", got.Status)
	}

	// 2. Simulate Leave Update (Status = LEFT)
	p.Status = LEFT
	p.Connected = false
	bLeft, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-leaving"), bLeft)

	// 3. Verify it remains in the table with Status=LEFT so TombstoneManager can
	//    find it for deferred cleanup.
	got, err = GetPeerFromTable("peer-leaving")
	if err != nil {
		t.Fatalf("expected LEFT peer to still be in table, got error: %v", err)
	}
	if got.Status != LEFT {
		t.Fatalf("expected peer status LEFT, got %s", got.Status)
	}
	if got.Connected {
		t.Fatal("expected Connected=false for LEFT peer")
	}
}

func TestPublicPortStoredInPeer(t *testing.T) {
	_ = GetAllPeers()

	p1 := Peer{ID: "head-1", PublicAddress: "1.2.3.4", PublicPort: "43905", Connected: true}
	b1, _ := json.Marshal(p1)
	UpdateNodeTableHook(ds.NewKey("head-1"), b1)

	p2 := Peer{ID: "relay-1", PublicAddress: "5.6.7.8", PublicPort: "18905", Connected: true}
	b2, _ := json.Marshal(p2)
	UpdateNodeTableHook(ds.NewKey("relay-1"), b2)

	got1, err := GetPeerFromTable("head-1")
	if err != nil {
		t.Fatalf("expected head-1 in table: %v", err)
	}
	if got1.PublicPort != "43905" {
		t.Fatalf("expected PublicPort=43905, got %s", got1.PublicPort)
	}

	got2, err := GetPeerFromTable("relay-1")
	if err != nil {
		t.Fatalf("expected relay-1 in table: %v", err)
	}
	if got2.PublicPort != "18905" {
		t.Fatalf("expected PublicPort=18905, got %s", got2.PublicPort)
	}
}

func TestRelayRoleAndPortStoredInPeer(t *testing.T) {
	_ = GetAllPeers()

	// Simulate a relay peer arriving via CRDT replication.
	p := Peer{
		ID:            "relay-node",
		PublicAddress: "10.0.0.1",
		PublicPort:    "18905",
		Role:          []string{"relay"},
		Connected:     true,
	}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("relay-node"), b)

	got, err := GetPeerFromTable("relay-node")
	if err != nil {
		t.Fatalf("expected relay-node in table: %v", err)
	}
	if len(got.Role) == 0 || got.Role[0] != "relay" {
		t.Fatalf("expected role=[relay], got %v", got.Role)
	}
	if got.PublicAddress != "10.0.0.1" {
		t.Fatalf("expected PublicAddress=10.0.0.1, got %s", got.PublicAddress)
	}
	if got.PublicPort != "18905" {
		t.Fatalf("expected PublicPort=18905, got %s", got.PublicPort)
	}
}

func TestNodeLeaveAndRejoin(t *testing.T) {
	// 1. Peer joins
	p := Peer{ID: "peer-rejoin", PublicAddress: "10.0.0.2", Status: CONNECTED, Connected: true}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), b)

	got, err := GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected peer to be in table after join")
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected CONNECTED, got %s", got.Status)
	}

	// 2. Peer leaves
	p.Status = LEFT
	p.Connected = false
	bLeft, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), bLeft)

	got, err = GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected LEFT peer to remain in table")
	}
	if got.Status != LEFT {
		t.Fatalf("expected LEFT, got %s", got.Status)
	}

	// 3. Peer rejoins — a non-LEFT update overwrites the LEFT status
	p.Status = CONNECTED
	p.Connected = true
	p.PublicAddress = "10.0.0.3" // new address after rejoin
	bRejoin, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), bRejoin)

	got, err = GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected peer to be in table after rejoin, got error: %v", err)
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected CONNECTED after rejoin, got %s", got.Status)
	}
	if !got.Connected {
		t.Fatal("expected Connected=true after rejoin")
	}
	if got.PublicAddress != "10.0.0.3" {
		t.Fatalf("expected updated public address 10.0.0.3, got %s", got.PublicAddress)
	}
}

func TestGetSelf(t *testing.T) {
	myself = Peer{ID: "QmTestSelf", Role: []string{"relay"}, PublicAddress: "1.2.3.4"}
	got := GetSelf()
	assert.Equal(t, "QmTestSelf", got.ID)
	assert.Equal(t, "1.2.3.4", got.PublicAddress)
	assert.Equal(t, []string{"relay"}, got.Role)
}

func TestSetMyselfForTest(t *testing.T) {
	SetMyselfForTest(Peer{ID: "QmTestSet"})
	assert.Equal(t, "QmTestSet", GetSelf().ID)
}

func TestRegisterRemotePeer_Signature(t *testing.T) {
	// Verify the function exists with the correct signature by assigning it.
	// Full integration test requires CRDT store which is tested in Task 8.
	fn := RegisterRemotePeer
	_ = fn
}

// LastSeen must mean "last proven alive", not "last time we processed any
// update about this peer". The staleness sweep in clock.go reasons on this
// field, so a hook that restamps it on every update — including updates that
// record a FAILED liveness check — guarantees the sweep can never fire. That is
// half of why dead workers stayed connected:true for 13h.
func TestUpdateNodeTableHook_DoesNotRestampLastSeen(t *testing.T) {
	_ = GetAllPeers()
	const proven = int64(1000)

	p := Peer{ID: "peer-lastseen-preserved", LastSeen: proven, Connected: false}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-lastseen-preserved"), b)

	got, err := GetPeerFromTable("peer-lastseen-preserved")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.LastSeen != proven {
		t.Fatalf("LastSeen = %d, want %d — the hook must carry the caller's value, not restamp to now", got.LastSeen, proven)
	}
}

// A peer we have never seen before still needs a sane LastSeen, otherwise the
// sweep's `LastSeen > 0` guard skips it forever.
func TestUpdateNodeTableHook_InitializesLastSeenForNewPeer(t *testing.T) {
	_ = GetAllPeers()
	before := time.Now().Unix()

	p := Peer{ID: "peer-lastseen-new"}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-lastseen-new"), b)

	got, err := GetPeerFromTable("peer-lastseen-new")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.LastSeen < before {
		t.Fatalf("LastSeen = %d, want >= %d — a first sighting should be stamped", got.LastSeen, before)
	}
}

// A head that proves a worker is gone must be able to tell the other heads,
// otherwise a head that cannot reach that worker's relay keeps serving the
// ghost forever (observed: ocf-2 holds QmXbXAnu9XVv NotConnected, so its own
// probe can never reach a verdict).
func TestUpdateNodeTableHook_EvictionFromHeadIsAccepted(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "evict-head-1", Peer{ID: "evict-head-1", Role: []string{"head"}})
	seedPeer(t, "evict-worker-1", Peer{
		ID: "evict-worker-1", Connected: true, LastSeen: 5000,
		Service: []Service{{Name: "llm"}},
	})

	evict := Peer{ID: "evict-worker-1", Status: LEFT, EvictedBy: "evict-head-1", LastSeen: 5000}
	b, _ := json.Marshal(evict)
	UpdateNodeTableHook(ds.NewKey("evict-worker-1"), b)

	got, err := GetPeerFromTable("evict-worker-1")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.Connected {
		t.Fatal("expected the worker to be marked disconnected by the head's eviction record")
	}
}

// Anyone can write any key in the CRDT, so an eviction record naming a peer we
// do not know to be a head is not something to act on. This does not make
// eviction authenticated — EvictedBy is self-reported — but it does stop a
// misconfigured or buggy non-head node from retiring healthy workers.
func TestUpdateNodeTableHook_EvictionFromNonHeadIsIgnored(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "evict-worker-2", Peer{
		ID: "evict-worker-2", Connected: true, LastSeen: 5000,
		Service: []Service{{Name: "llm"}},
	})
	seedPeer(t, "evict-notahead", Peer{ID: "evict-notahead", Role: []string{"worker"}})

	evict := Peer{ID: "evict-worker-2", Status: LEFT, EvictedBy: "evict-notahead", LastSeen: 5000}
	b, _ := json.Marshal(evict)
	UpdateNodeTableHook(ds.NewKey("evict-worker-2"), b)

	got, err := GetPeerFromTable("evict-worker-2")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got.Connected {
		t.Fatal("a non-head must not be able to evict a peer")
	}
}

// An evictor we have never heard of cannot be confirmed to be a head, so the
// record is ignored rather than trusted.
func TestUpdateNodeTableHook_EvictionFromUnknownPeerIsIgnored(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "evict-worker-3", Peer{
		ID: "evict-worker-3", Connected: true, LastSeen: 5000,
		Service: []Service{{Name: "llm"}},
	})

	evict := Peer{ID: "evict-worker-3", Status: LEFT, EvictedBy: "evict-ghost-head", LastSeen: 5000}
	b, _ := json.Marshal(evict)
	UpdateNodeTableHook(ds.NewKey("evict-worker-3"), b)

	got, err := GetPeerFromTable("evict-worker-3")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got.Connected {
		t.Fatal("an unverifiable evictor must not be able to evict a peer")
	}
}

// A peer announcing its own departure carries no EvictedBy and must keep working
// exactly as before — this is the normal, graceful AnnounceLeave path.
func TestUpdateNodeTableHook_SelfAnnouncedLeaveStillWorks(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "evict-worker-4", Peer{
		ID: "evict-worker-4", Connected: true, LastSeen: 5000,
		Service: []Service{{Name: "llm"}},
	})

	leave := Peer{ID: "evict-worker-4", Status: LEFT, LastSeen: 5000}
	b, _ := json.Marshal(leave)
	UpdateNodeTableHook(ds.NewKey("evict-worker-4"), b)

	got, err := GetPeerFromTable("evict-worker-4")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.Connected {
		t.Fatal("a self-announced LEFT must still mark the peer disconnected")
	}
}

func seedPeer(t *testing.T, key string, p Peer) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal seed peer: %v", err)
	}
	UpdateNodeTableHook(ds.NewKey(key), b)
}

// LastSeen must be monotonic. Now that the hook carries the caller's value
// instead of restamping, a CRDT rebroadcast holding an old LastSeen would
// otherwise drag a peer's timestamp backwards past locally-proven evidence of
// life. Observed in production: a peer pinged 7s ago briefly read as 27h stale
// when its own rebroadcast record arrived, which for a service-less peer is
// enough to trip the 2-minute disconnect window and flap it.
func TestUpdateNodeTableHook_LastSeenNeverGoesBackwards(t *testing.T) {
	_ = GetAllPeers()
	recent := time.Now().Unix()

	b, _ := json.Marshal(Peer{ID: "peer-monotonic", LastSeen: recent, Connected: true})
	UpdateNodeTableHook(ds.NewKey("peer-monotonic"), b)

	// A rebroadcast carrying a much older timestamp for the same peer.
	stale, _ := json.Marshal(Peer{ID: "peer-monotonic", LastSeen: recent - 90000, Connected: true})
	UpdateNodeTableHook(ds.NewKey("peer-monotonic"), stale)

	got, err := GetPeerFromTable("peer-monotonic")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.LastSeen != recent {
		t.Fatalf("LastSeen = %d, want %d — a stale record must not overwrite newer proof of life", got.LastSeen, recent)
	}
}

// An eviction published by the peer's own relay must be accepted even when the
// evictor is not (known to be) a head: the record itself carries the match
// (EvictedBy == RelayPeer), and the relay is the authority on whether the
// worker is still attached. This is what lets a dispatcher that runs the relay
// in-process — role "worker" — converge its verdicts mesh-wide.
func TestUpdateNodeTableHook_EvictionFromOwnRelayIsAccepted(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "relay-self-1", Peer{ID: "relay-self-1", Role: []string{"worker"}})
	seedPeer(t, "relay-worker-1", Peer{
		ID: "relay-worker-1", Connected: true, LastSeen: 5000,
		RelayPeer: "relay-self-1",
		Service:   []Service{{Name: "llm"}},
	})

	evict := Peer{ID: "relay-worker-1", Status: LEFT, EvictedBy: "relay-self-1", RelayPeer: "relay-self-1", LastSeen: 5000}
	b, _ := json.Marshal(evict)
	UpdateNodeTableHook(ds.NewKey("relay-worker-1"), b)

	got, err := GetPeerFromTable("relay-worker-1")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.Connected {
		t.Fatal("expected the worker to be marked disconnected by its own relay's eviction record")
	}
}

// The relay-match exemption must not become a loophole: an evictor that merely
// shares the relay's ID string but does not match the peer's advertised
// RelayPeer is still subject to the known-head requirement.
func TestUpdateNodeTableHook_EvictionByNonRelayNonHeadStillIgnored(t *testing.T) {
	_ = GetAllPeers()
	seedPeer(t, "relay-self-2", Peer{ID: "relay-self-2", Role: []string{"worker"}})
	seedPeer(t, "relay-worker-2", Peer{
		ID: "relay-worker-2", Connected: true, LastSeen: 5000,
		RelayPeer: "some-other-relay",
		Service:   []Service{{Name: "llm"}},
	})

	evict := Peer{ID: "relay-worker-2", Status: LEFT, EvictedBy: "relay-self-2", RelayPeer: "some-other-relay", LastSeen: 5000}
	b, _ := json.Marshal(evict)
	UpdateNodeTableHook(ds.NewKey("relay-worker-2"), b)

	got, err := GetPeerFromTable("relay-worker-2")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got.Connected {
		t.Fatal("an evictor that is neither a known head nor the peer's relay must not evict")
	}
}

// DeletePeerRow is the operator escape hatch for ghost rows: it must remove
// the row locally, refuse the node's own row, and fail cleanly for unknown
// peers. With no CRDT store it stays local-only (sweeps converge the rest).
func TestDeletePeerRow(t *testing.T) {
	_ = GetAllPeers()
	oldMyID := MyID
	defer func() { MyID = oldMyID }()
	MyID = "self-node"

	seedPeer(t, "ghost-worker", Peer{ID: "ghost-worker", Connected: true, LastSeen: 5000, Service: []Service{{Name: "llm"}}})

	if err := DeletePeerRow(""); err == nil {
		t.Fatal("empty peer id must be rejected")
	}
	if err := DeletePeerRow("self-node"); err == nil {
		t.Fatal("deleting the node's own row must be refused")
	}
	if err := DeletePeerRow("never-seen"); err == nil {
		t.Fatal("deleting an unknown peer must fail")
	}
	if err := DeletePeerRow("ghost-worker"); err != nil {
		t.Fatalf("deleting a seeded ghost must succeed, got: %v", err)
	}
	if _, err := GetPeerFromTable("ghost-worker"); err == nil {
		t.Fatal("row must be gone from the table after DeletePeerRow")
	}
}
