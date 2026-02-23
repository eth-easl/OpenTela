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
	table := getNodeTable()

	// Ensure we start clean for this test
	for k := range *table {
		delete(*table, k)
	}

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
	// No sleep needed, math is on Unix timestamps

	count, err := tm.CleanupLeftNodes(context.Background())
	if err != nil {
		t.Fatalf("CleanupLeftNodes failed: %v", err)
	}

	// 5. Verify results
	if count != 1 {
		t.Errorf("Expected 1 node to be removed, got %d", count)
	}

	// Verify 'active-node' is still in table (Cleanup doesn't remove from table, it removes from DATASTORE)
	// Wait, code says:
	// func (tm *TombstoneManager) CleanupLeftNodes(ctx context.Context) (int, error) {
	// ... candidates := tm.collectCandidates()
	// ... tm.store.Delete(ctx, ds.NewKey(key))
	// It deletes from STORE. It does NOT remove from 'table' in memory explicitly in that function?
	// Ah, usually the CRDT store update might trigger a hook to update the generic table?
	// `CleanupLeftNodes` only calls `store.Delete`.

	// Let's verify store state.
	// Active node was not put in store in this setup, only the Left ones were.

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
