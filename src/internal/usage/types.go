package usage

// UsageRecord represents a single usage measurement. The head records one per
// served request (see Tracker) for local accounting and dispute evidence; the
// authoritative billing settlement happens in the API market's billing gateway
// (api.opentela.ai), not from these local records.
type UsageRecord struct {
	RequestID    string // Unique identifier for the request
	Service      string // Service name (e.g., "llm", "sandbox")
	ConsumerPeer string // Head node (dispatcher) peer ID
	ProviderPeer string // Worker node (provider) peer ID
	MetricName   string // Metric type (e.g., "tokens", "gpu_ms")
	MetricValue  int64  // Measured value
	Timestamp    int64  // Unix timestamp
}

const (
	// Header prefix for usage metrics
	UsageHeaderPrefix = "X-Usage-"
)
