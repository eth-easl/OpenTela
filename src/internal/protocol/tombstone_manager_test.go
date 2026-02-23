package protocol

import (
	"context"
	"sync"
	"testing"
	"time"

	crdt "opentela/internal/protocol/go-ds-crdt"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

type MockDAGService struct {
	nodes map[cid.Cid]ipld.Node
	mu    sync.RWMutex
}

func NewMockDAGService() *MockDAGService {
	return &MockDAGService{
		nodes: make(map[cid.Cid]ipld.Node),
	}
}

func (m *MockDAGService) Add(ctx context.Context, n ipld.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[n.Cid()] = n
	return nil
}

func (m *MockDAGService) AddMany(ctx context.Context, nodes []ipld.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range nodes {
		m.nodes[n.Cid()] = n
	}
	return nil
}

func (m *MockDAGService) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if n, ok := m.nodes[c]; ok {
		return n, nil
	}
	return nil, ipld.ErrNotFound{Cid: c}
}

func (m *MockDAGService) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	ch := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(ch)
		for _, c := range cids {
			n, err := m.Get(ctx, c)
			ch <- &ipld.NodeOption{Node: n, Err: err}
		}
	}()
	return ch
}

func (m *MockDAGService) Remove(ctx context.Context, c cid.Cid) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, c)
	return nil
}

func (m *MockDAGService) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range cids {
		delete(m.nodes, c)
	}
	return nil
}

// cleanNodeTable is a test helper that empties the global in-memory node table.
func cleanNodeTable() *NodeTable {
	table := getNodeTable()
	for k := range *table {
		delete(*table, k)
	}
	return table
}

func TestCleanupLeftNodes(t *testing.T) {
	// 1. Setup in-memory datastore and mock DAG service
	store := dssync.MutexWrap(ds.NewMapDatastore())
	mockDAG := NewMockDAGService()
	crdtStore, _ := crdt.New(store, ds.NewKey("/test"), mockDAG, nil, crdt.DefaultOptions())

	// 2. Initialize TombstoneManager with retention > 1s (Unix time is in seconds)
	retention := 2 * time.Second
	tm := &TombstoneManager{
		store:     crdtStore,
		retention: retention,
	}

	// Initialize the global node table if not already
	table := cleanNodeTable()

	// Case 1: Active Node (Status=CONNECTED, Recent)
	activePeer := Peer{
		ID:        "active-node",
		Status:    CONNECTED,
		LastSeen:  time.Now().Unix(),
		Connected: true,
	}
	(*table)["/active-node"] = activePeer

	// Case 2: Left Node, recent (Should NOT be cleaned up)
	recentLeftPeer := Peer{
		ID:     "recent-left-node",
		Status: LEFT,
		// LastSeen within retention period (e.g. 1s ago < 2s)
		LastSeen: time.Now().Add(-1 * time.Second).Unix(),
	}
	(*table)["/recent-left-node"] = recentLeftPeer
	keyRecent := ds.NewKey("recent-left-node")
	_ = crdtStore.Put(context.Background(), keyRecent, []byte("some-data"))

	// Case 3: Left Node, old (Should be cleaned up)
	oldLeftPeer := Peer{
		ID:     "old-left-node",
		Status: LEFT,
		// LastSeen older than retention (e.g. 5s ago > 2s)
		LastSeen: time.Now().Add(-5 * time.Second).Unix(),
	}
	(*table)["/old-left-node"] = oldLeftPeer
	keyOld := ds.NewKey("old-left-node")
	if err := crdtStore.Put(context.Background(), keyOld, []byte("some-data")); err != nil {
		t.Fatalf("Failed to put old node: %v", err)
	}

	// Verify it exists
	if has, _ := crdtStore.Has(context.Background(), keyOld); !has {
		t.Fatal("Old node not found in store before cleanup")
	}

	// 4. Run Cleanup
	count, err := tm.CleanupLeftNodes(context.Background())
	if err != nil {
		t.Fatalf("CleanupLeftNodes failed: %v", err)
	}

	// 5. Verify results
	if count != 1 {
		t.Errorf("Expected 1 node to be removed, got %d", count)
	}

	// Verify 'active-node' is still in table
	if _, ok := (*table)["/active-node"]; !ok {
		t.Error("Active node should still be in the in-memory table")
	}

	// Verify 'recent-left-node' is still in table (not yet past retention)
	if _, ok := (*table)["/recent-left-node"]; !ok {
		t.Error("Recent left node should still be in the in-memory table")
	}

	// Verify 'old-left-node' was removed from the in-memory table
	if _, ok := (*table)["/old-left-node"]; ok {
		t.Error("Old left node should have been removed from the in-memory table after cleanup")
	}

	// Check recent left node is still in store
	hasRecent, _ := crdtStore.Has(context.Background(), keyRecent)
	if !hasRecent {
		t.Error("Recent left node should still be in datastore")
	}

	// Check old left node is gone from store
	hasOld, _ := crdtStore.Has(context.Background(), keyOld)
	if hasOld {
		t.Error("Old left node should have been deleted from datastore")
	}
}

// TestRejoinAfterLeave verifies that a node which was marked LEFT can rejoin
// the network and be treated as an active peer again.  Critically, the
// TombstoneManager must NOT clean up a node that has already rejoined.
func TestRejoinAfterLeave(t *testing.T) {
	// 1. Setup
	store := dssync.MutexWrap(ds.NewMapDatastore())
	mockDAG := NewMockDAGService()
	crdtStore, _ := crdt.New(store, ds.NewKey("/test-rejoin"), mockDAG, nil, crdt.DefaultOptions())

	retention := 2 * time.Second
	tm := &TombstoneManager{
		store:     crdtStore,
		retention: retention,
	}

	table := cleanNodeTable()
	ctx := context.Background()
	peerID := "rejoin-peer"
	peerKey := "/" + peerID

	// 2. Peer joins initially — write data into CRDT store
	keyDS := ds.NewKey(peerID)
	if err := crdtStore.Put(ctx, keyDS, []byte("initial-data")); err != nil {
		t.Fatalf("Failed to put initial data: %v", err)
	}
	(*table)[peerKey] = Peer{
		ID:        peerID,
		Status:    CONNECTED,
		LastSeen:  time.Now().Unix(),
		Connected: true,
	}

	// 3. Peer leaves — mark as LEFT with an old LastSeen (past retention)
	(*table)[peerKey] = Peer{
		ID:        peerID,
		Status:    LEFT,
		Connected: false,
		LastSeen:  time.Now().Add(-5 * time.Second).Unix(),
	}

	// Sanity: collectCandidates should find it
	candidates := tm.collectCandidates()
	if len(candidates) != 1 {
		t.Fatalf("Expected 1 candidate before rejoin, got %d", len(candidates))
	}

	// 4. Peer rejoins BEFORE cleanup runs — status is no longer LEFT
	if err := crdtStore.Put(ctx, keyDS, []byte("rejoined-data")); err != nil {
		t.Fatalf("Failed to put rejoin data: %v", err)
	}
	(*table)[peerKey] = Peer{
		ID:        peerID,
		Status:    CONNECTED,
		LastSeen:  time.Now().Unix(),
		Connected: true,
	}

	// 5. Run cleanup — the rejoined peer must NOT be collected
	candidates = tm.collectCandidates()
	if len(candidates) != 0 {
		t.Errorf("Expected 0 candidates after rejoin, got %d", len(candidates))
	}

	count, err := tm.CleanupLeftNodes(ctx)
	if err != nil {
		t.Fatalf("CleanupLeftNodes failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 removals after rejoin, got %d", count)
	}

	// 6. Verify data is still in the CRDT store
	has, _ := crdtStore.Has(ctx, keyDS)
	if !has {
		t.Error("Rejoined peer's data should still be in the CRDT store")
	}
	val, err := crdtStore.Get(ctx, keyDS)
	if err != nil {
		t.Fatalf("Failed to get rejoined peer's data: %v", err)
	}
	if string(val) != "rejoined-data" {
		t.Errorf("Expected rejoined-data, got %s", string(val))
	}

	// 7. Verify peer is still in the in-memory table as CONNECTED
	p, ok := (*table)[peerKey]
	if !ok {
		t.Fatal("Rejoined peer should be in the in-memory table")
	}
	if p.Status != CONNECTED {
		t.Errorf("Rejoined peer status should be CONNECTED, got %s", p.Status)
	}
	if !p.Connected {
		t.Error("Rejoined peer should be marked Connected=true")
	}
}

// TestRejoinAfterCleanup verifies the scenario where a LEFT node is fully
// cleaned up (deleted from the CRDT store and in-memory table) and then the
// same peer ID rejoins.  The new Put must succeed and the peer must appear in
// the table again.
func TestRejoinAfterCleanup(t *testing.T) {
	// 1. Setup
	store := dssync.MutexWrap(ds.NewMapDatastore())
	mockDAG := NewMockDAGService()
	crdtStore, _ := crdt.New(store, ds.NewKey("/test-rejoin-after-cleanup"), mockDAG, nil, crdt.DefaultOptions())

	retention := 1 * time.Second
	tm := &TombstoneManager{
		store:     crdtStore,
		retention: retention,
	}

	table := cleanNodeTable()
	ctx := context.Background()
	peerID := "cleanup-rejoin-peer"
	peerKey := "/" + peerID
	keyDS := ds.NewKey(peerID)

	// 2. Peer joins and then leaves
	if err := crdtStore.Put(ctx, keyDS, []byte("original")); err != nil {
		t.Fatalf("Failed to put: %v", err)
	}
	(*table)[peerKey] = Peer{
		ID:        peerID,
		Status:    LEFT,
		Connected: false,
		LastSeen:  time.Now().Add(-5 * time.Second).Unix(),
	}

	// 3. Cleanup runs — peer is deleted from store and table
	count, err := tm.CleanupLeftNodes(ctx)
	if err != nil {
		t.Fatalf("CleanupLeftNodes failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 removal, got %d", count)
	}

	// Verify gone from store
	if has, _ := crdtStore.Has(ctx, keyDS); has {
		t.Fatal("Peer should have been deleted from CRDT store")
	}
	// Verify gone from table
	if _, ok := (*table)[peerKey]; ok {
		t.Fatal("Peer should have been removed from in-memory table")
	}

	// 4. Same peer ID rejoins — new Put with fresh data
	if err := crdtStore.Put(ctx, keyDS, []byte("comeback")); err != nil {
		t.Fatalf("Failed to put rejoin data after cleanup: %v", err)
	}
	(*table)[peerKey] = Peer{
		ID:        peerID,
		Status:    CONNECTED,
		LastSeen:  time.Now().Unix(),
		Connected: true,
	}

	// 5. Verify the new data is visible
	has, _ := crdtStore.Has(ctx, keyDS)
	if !has {
		t.Error("Rejoined peer's data should be in the CRDT store after cleanup+rejoin")
	}
	val, err := crdtStore.Get(ctx, keyDS)
	if err != nil {
		t.Fatalf("Failed to get data: %v", err)
	}
	if string(val) != "comeback" {
		t.Errorf("Expected 'comeback', got %s", string(val))
	}

	// 6. Cleanup should NOT touch the rejoined peer
	count, err = tm.CleanupLeftNodes(ctx)
	if err != nil {
		t.Fatalf("CleanupLeftNodes after rejoin failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 removals after rejoin, got %d", count)
	}

	// Peer is still connected
	p, ok := (*table)[peerKey]
	if !ok {
		t.Fatal("Rejoined peer should still be in the table")
	}
	if p.Status != CONNECTED {
		t.Errorf("Expected CONNECTED, got %s", p.Status)
	}
}
