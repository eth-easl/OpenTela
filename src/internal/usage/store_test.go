package usage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *UsageStore {
	t.Helper()
	store, err := NewUsageStore(filepath.Join(t.TempDir(), "usage"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestUsageStore_SaveAndGet(t *testing.T) {
	store := newTestStore(t)

	rec := &UsageRecord{
		RequestID:    "req-1",
		Service:      "llm",
		ConsumerPeer: "head",
		ProviderPeer: "worker",
		MetricName:   "tokens",
		MetricValue:  1500,
		Timestamp:    1700000000,
	}
	require.NoError(t, store.SaveRecord(rec))

	got, err := store.GetRecord("req-1", "tokens")
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

func TestUsageStore_GetRecord_NotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetRecord("missing", "tokens")
	require.Error(t, err, "a missing record must error, not return a zero record")
}
