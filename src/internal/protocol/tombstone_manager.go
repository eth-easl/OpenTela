package protocol

import (
	"context"
	"sync"
	"time"

	"ocf/internal/common"
	crdt "ocf/internal/protocol/go-ds-crdt"

	ds "github.com/ipfs/go-datastore"
)

type TombstoneManager struct {
	store     *crdt.Datastore
	retention time.Duration
}

var (
	tmOnce sync.Once
	tm     *TombstoneManager
)

func GetTombstoneManager(store *crdt.Datastore) *TombstoneManager {
	tmOnce.Do(func() {
		retention := readDurationSetting("crdt.tombstone_retention", defaultTombstoneRetention)
		tm = &TombstoneManager{
			store:     store,
			retention: retention,
		}
	})
	return tm
}

// CleanupLeftNodes scans the node table for peers with StatusLeft and removes them
// if they have been left for longer than the retention period.
func (tm *TombstoneManager) CleanupLeftNodes(ctx context.Context) (int, error) {
	// 1. Collect candidates
	candidates := tm.collectCandidates()

	removedCount := 0
	for _, key := range candidates {
		// key is raw, e.g. "Qm..." or "/Qm..."
		// ds.NewKey handles standardizing it.
		if err := tm.store.Delete(ctx, ds.NewKey(key)); err != nil {
			common.Logger.Errorf("Failed to delete left node %s: %v", key, err)
		} else {
			removedCount++
		}
	}

	return removedCount, nil
}

func (tm *TombstoneManager) collectCandidates() []string {
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }()

	table := *getNodeTable()
	limit := time.Now().Add(-tm.retention).Unix()
	var candidates []string

	for key, peer := range table {
		if peer.Status == LEFT && peer.LastSeen < limit {
			candidates = append(candidates, key)
		}
	}
	return candidates
}
