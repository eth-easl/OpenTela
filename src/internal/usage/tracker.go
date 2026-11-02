// src/internal/usage/tracker.go
package usage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"opentela/internal/common"

	"github.com/google/uuid"
)

var (
	globalTracker *Tracker
	trackerOnce   sync.Once
)

// Tracker manages usage tracking with a local store
type Tracker struct {
	store *UsageStore
}

// InitTracker initializes the global tracker instance
func InitTracker() error {
	var initErr error
	trackerOnce.Do(func() {
		dataDir := common.DataDir()
		if dataDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				initErr = fmt.Errorf("getting home dir: %w", err)
				return
			}
			dataDir = filepath.Join(homeDir, ".otela")
		}
		usageDir := filepath.Join(dataDir, "usage")

		store, err := NewUsageStore(usageDir)
		if err != nil {
			initErr = fmt.Errorf("creating usage store: %w", err)
			return
		}

		globalTracker = &Tracker{store: store}
	})
	return initErr
}

// CloseTracker closes the global tracker
func CloseTracker() error {
	if globalTracker != nil && globalTracker.store != nil {
		return globalTracker.store.Close()
	}
	return nil
}

// Track records a single usage metric
func Track(requestID, service, consumerPeer, providerPeer, metricName string, metricValue int64) error {
	if globalTracker == nil {
		if err := InitTracker(); err != nil {
			return fmt.Errorf("initializing tracker: %w", err)
		}
	}

	record := &UsageRecord{
		RequestID:    requestID,
		Service:      service,
		ConsumerPeer: consumerPeer,
		ProviderPeer: providerPeer,
		MetricName:   metricName,
		MetricValue:  metricValue,
		Timestamp:    time.Now().Unix(),
	}

	if err := globalTracker.store.SaveRecord(record); err != nil {
		return fmt.Errorf("saving usage record: %w", err)
	}

	common.Logger.Debugf("Tracked usage: requestID=%s service=%s metric=%s value=%d",
		requestID, service, metricName, metricValue)
	return nil
}

// GenerateRequestID creates a unique request identifier
func GenerateRequestID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])
}
