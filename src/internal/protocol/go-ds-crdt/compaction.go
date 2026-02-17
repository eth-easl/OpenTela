package crdt

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

const tombstoneTimestampBytes = 8

func decodeTombstoneTimestamp(value []byte) time.Time {
	if len(value) < tombstoneTimestampBytes {
		return time.Time{}
	}

	seconds := binary.BigEndian.Uint64(value[:tombstoneTimestampBytes])
	if seconds == 0 {
		return time.Time{}
	}

	return time.Unix(int64(seconds), 0).UTC()
}

// CompactTombstones prunes tombstone entries whose timestamp is older than the provided retention period.
// The method removes the tombstone marker as well as the corresponding element pointer to release space.
// If limit > 0, no more than limit entries are removed per invocation.
func (store *Datastore) CompactTombstones(ctx context.Context, olderThan time.Duration, limit int) (int, error) {
	if olderThan <= 0 {
		return 0, errors.New("olderThan must be positive")
	}

	prefix := store.set.keyPrefix(tombsNs)
	results, err := store.store.Query(ctx, query.Query{
		Prefix: prefix.String(),
	})
	if err != nil {
		return 0, err
	}
	defer results.Close()

	deadline := time.Now().Add(-olderThan)
	removed := 0

	var write ds.Write = store.store
	var batch ds.Batch
	if batchingDs, ok := store.store.(ds.Batching); ok {
		batch, err = batchingDs.Batch(ctx)
		if err != nil {
			return 0, err
		}
		write = batch
	}

	for limit <= 0 || removed < limit {

		var (
			res query.Result
			ok  bool
		)
		select {
		case <-ctx.Done():
			return removed, ctx.Err()
		case res, ok = <-results.Next():
			if !ok {
				ok = false
			}
		}

		if !ok {
			break
		}

		if res.Error != nil {
			return removed, res.Error
		}

		if limit > 0 && removed >= limit {
			break
		}

		tombTime := decodeTombstoneTimestamp(res.Value)
		if !tombTime.IsZero() && tombTime.After(deadline) {
			continue
		}

		tombKey := ds.NewKey(res.Key)
		namespaces := tombKey.Namespaces()
		if len(namespaces) < 5 {
			continue
		}

		blockID := namespaces[len(namespaces)-1]
		if blockID == "" {
			continue
		}

		elemComponents := namespaces[3 : len(namespaces)-1]
		if len(elemComponents) == 0 {
			continue
		}

		elemKey := "/" + strings.Join(elemComponents, "/")

		if err := write.Delete(ctx, store.set.tombsPrefix(elemKey).ChildString(blockID)); err != nil && !errors.Is(err, ds.ErrNotFound) {
			return removed, err
		}

		if err := write.Delete(ctx, store.set.elemsPrefix(elemKey).ChildString(blockID)); err != nil && !errors.Is(err, ds.ErrNotFound) {
			return removed, err
		}

		removed++
	}

	if batch != nil {
		if err := batch.Commit(ctx); err != nil {
			return removed, err
		}
	}

	return removed, nil
}
