package protocol

import (
	"sync"
	"time"

	"ocf/internal/common"
	crdt "ocf/internal/protocol/go-ds-crdt"

	"github.com/spf13/viper"
)

const (
	defaultTombstoneRetention          = 24 * time.Hour
	defaultTombstoneCompactionInterval = time.Hour
	defaultTombstoneCompactionBatch    = 512
)

var tombstoneCompactorOnce sync.Once

func startTombstoneCompactor(store *crdt.Datastore) {
	tombstoneCompactorOnce.Do(func() {
		retention := readDurationSetting("crdt.tombstone_retention", defaultTombstoneRetention)
		interval := readDurationSetting("crdt.tombstone_compaction_interval", defaultTombstoneCompactionInterval)
		batch := viper.GetInt("crdt.tombstone_compaction_batch")
		if batch <= 0 {
			batch = defaultTombstoneCompactionBatch
		}

		if retention <= 0 {
			common.Logger.Info("Tombstone compaction disabled (retention <= 0)")
			return
		}
		if interval <= 0 {
			interval = defaultTombstoneCompactionInterval
		}

		// Initialize TombstoneManager
		tm := GetTombstoneManager(store)

		ctx := store.Context()
		go func() {
			run := func() {
				// 1. Clean up "Left" nodes (Application level tombstones)
				if removedLeft, err := tm.CleanupLeftNodes(ctx); err != nil {
					common.Logger.Warnf("Left nodes cleanup failed: %v", err)
				} else if removedLeft > 0 {
					common.Logger.Infof("Cleaned up %d left nodes", removedLeft)
				}

				// 2. Compact CRDT tombstones (Storage level tombstones)
				removed, err := store.CompactTombstones(ctx, retention, batch)
				if err != nil {
					if ctx.Err() == nil {
						common.Logger.Warnf("Tombstone compaction failed: %v", err)
					}
					return
				}
				if removed > 0 {
					common.Logger.Infof("Compacted %d tombstone entries older than %s", removed, retention)
				}
			}

			// Run once shortly after startup to clean up any stale data.
			run()

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					run()
				}
			}
		}()
	})
}

func readDurationSetting(key string, fallback time.Duration) time.Duration {
	value := viper.GetDuration(key)
	if value <= 0 {
		return fallback
	}
	return value
}
