package usage

import (
	"encoding/json"
	"fmt"

	badger "github.com/dgraph-io/badger/v4"
)

// UsageStore persists usage records locally using BadgerDB
type UsageStore struct {
	db *badger.DB
}

// NewUsageStore creates a new usage store
func NewUsageStore(dir string) (*UsageStore, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil // Disable logging for tests

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("opening badger db: %w", err)
	}

	return &UsageStore{db: db}, nil
}

// Close closes the database
func (s *UsageStore) Close() error {
	return s.db.Close()
}

// SaveRecord persists a usage record
func (s *UsageStore) SaveRecord(record *UsageRecord) error {
	key := []byte(fmt.Sprintf("record/%s/%s", record.RequestID, record.MetricName))

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshalling record: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// GetRecord retrieves a usage record by request ID and metric name.
func (s *UsageStore) GetRecord(requestID string, metricName string) (*UsageRecord, error) {
	var record UsageRecord

	key := []byte(fmt.Sprintf("record/%s/%s", requestID, metricName))

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &record)
		})
	})

	if err != nil {
		return nil, err
	}

	return &record, nil
}
