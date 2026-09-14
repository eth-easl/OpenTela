package usage

import (
	"testing"
	"time"
)

func TestUsageRecordValidation(t *testing.T) {
	record := UsageRecord{
		RequestID:    "test-req-1",
		Service:      "llm",
		ConsumerPeer: "head-peer-id",
		ProviderPeer: "worker-peer-id",
		MetricName:   "tokens",
		MetricValue:  1000,
		Timestamp:    time.Now().Unix(),
	}

	if record.RequestID == "" {
		t.Error("RequestID should not be empty")
	}
	if record.MetricValue <= 0 {
		t.Error("MetricValue should be positive")
	}
}
