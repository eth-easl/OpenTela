package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"opentela/internal/common"
	"opentela/internal/protocol"
	"opentela/internal/usage"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axiomhq/axiom-go/axiom"
	"github.com/axiomhq/axiom-go/axiom/ingest"
	"github.com/buger/jsonparser"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
)

var (
	globalTransport  *http.Transport
	trustedTransport *http.Transport
	transportOnce    sync.Once
	trustedOnce      sync.Once

	routingRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "otela_routing_requests_total",
			Help: "Total number of requests forwarded to workers",
		},
		[]string{"service", "status"},
	)
	routingRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "otela_routing_request_duration_seconds",
			Help:    "End-to-end forwarding latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service"},
	)
	routingFallbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "otela_routing_fallback_total",
			Help: "Number of times each fallback tier was used",
		},
		[]string{"service", "level"},
	)
	routingRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "otela_routing_retries_total",
			Help: "Retry outcomes during request forwarding",
		},
		[]string{"service", "outcome"},
	)
)

func init() {
	prometheus.MustRegister(routingRequestsTotal, routingRequestDuration, routingFallbackTotal, routingRetriesTotal)
}

func getGlobalTransport() *http.Transport {
	transportOnce.Do(func() {
		node, _ := protocol.GetP2PNode(nil)
		globalTransport = &http.Transport{
			ResponseHeaderTimeout: 10 * time.Minute, // Allow up to 10 minutes for response headers
			IdleConnTimeout:       60 * time.Second, // Keep connections alive for 60 seconds
			DisableKeepAlives:     false,            // Enable keep-alives for better performance
			MaxIdleConns:          1024,             // Support large peer sets
			MaxIdleConnsPerHost:   64,               // Larger pool so bursty per-peer forwards don't
			//                                          have to open fresh libp2p streams (gostream.Dial
			//                                          over a relay circuit is non-trivial).
		}
		globalTransport.RegisterProtocol("libp2p", newLibp2pHTTPRoundTripper(node))
	})
	return globalTransport
}

func getTrustedTransport() *http.Transport {
	trustedOnce.Do(func() {
		node, _ := protocol.GetP2PNode(nil)
		trustedTransport = &http.Transport{
			ResponseHeaderTimeout: 10 * time.Minute,
			IdleConnTimeout:       60 * time.Second,
			DisableKeepAlives:     false,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   32,
		}
		trustedTransport.RegisterProtocol("libp2p", newTrustedLibp2pHTTPRoundTripper(node))
	})
	return trustedTransport
}

func ErrorHandler(res http.ResponseWriter, req *http.Request, err error) {
	if _, werr := fmt.Fprintf(res, "ERROR: %s", err.Error()); werr != nil {
		// The client is usually already gone when this write fails.
		common.Logger.Debug("Error writing error response: ", werr)
	}
}

// StreamAwareResponseWriter wraps the response writer to handle streaming
type StreamAwareResponseWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (s *StreamAwareResponseWriter) WriteHeader(statusCode int) {
	// Enable streaming headers if this is a streaming response
	if s.ResponseWriter.Header().Get("Content-Type") == "text/event-stream" {
		s.ResponseWriter.Header().Set("Cache-Control", "no-cache")
		s.ResponseWriter.Header().Set("Connection", "keep-alive")
		s.ResponseWriter.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	}
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *StreamAwareResponseWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// P2P handler for forwarding requests to other peers
func P2PForwardHandler(c *gin.Context) {
	// Set a longer timeout for AI/ML services
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()
	// Pass the context to the request
	c.Request = c.Request.WithContext(ctx)

	requestPeer := c.Param("peerId")
	requestPath := c.Param("path")
	clientWallet := ""
	if isControlPlaneEnabled() {
		decision, cpErr := authorizeRequestForPeers(ctx, c.GetHeader("Authorization"), []string{requestPeer})
		if cpErr != nil {
			c.JSON(cpErr.Status, gin.H{"error": cpErr.clientMessage()})
			return
		}
		if decision == nil || !decision.allows(requestPeer) {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied by instance ACL"})
			return
		}
		clientWallet = decision.PrimaryWallet
	}
	if clientWallet == "" {
		clientWallet = resolveClientWallet(c)
	}

	// Log event as before
	event := []axiom.Event{{ingest.TimestampField: time.Now(), "event": "P2P Forward", "from": &protocol.MyID, "to": requestPeer, "path": requestPath}}
	IngestEvents(event)

	target := url.URL{
		Scheme: "libp2p",
		Host:   requestPeer,
		Path:   requestPath,
	}
	common.Logger.Debugf("P2P forward: %s", target.String())

	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
		// req.URL.Host is what the libp2p RoundTripper dials, so it must be the
		// target peer. Assigning req.Host here instead uses the *incoming* Host
		// header, because req.Host is only set to target.Host on the next line.
		// (ServiceForwardHandler gets this right by assigning req.Host first.)
		req.URL.Host = target.Host
		req.Host = target.Host
		if clientWallet != "" {
			req.Header.Set("X-Otela-Client-Wallet", clientWallet)
		}
		// DO NOT read body here; httputil.ReverseProxy will stream it from c.Request.Body
	}

	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	proxy.Transport = getGlobalTransport()
	proxy.ErrorHandler = ErrorHandler
	proxy.ModifyResponse = rewriteHeader()
	proxy.ServeHTTP(c.Writer, c.Request)
}

func P2PServiceForwardHandler(c *gin.Context) {
	scope, err := newRoutingScope(partitionPermissionless, "", routeKindP2PIngress, c.Param("service"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p2pServiceForwardWithScope(c, scope)
}

func TrustedRegionP2PServiceForwardHandler(c *gin.Context) {
	scope, err := newRoutingScope(partitionTrustedRegion, c.Param("region"), routeKindP2PIngress, c.Param("service"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p2pServiceForwardWithScope(c, scope)
}

func p2pServiceForwardWithScope(c *gin.Context, scope routingScope) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)

	requestPeer := c.Param("peerId")
	requestPath := buildInternalServicePath(scope, c.Param("path"))
	clientWallet := ""
	forwardKeyHash := ""
	if isControlPlaneEnabled() {
		decision, cpErr := authorizeRequestForScope(ctx, c.GetHeader("Authorization"), scope, []string{requestPeer}, "")
		if cpErr != nil {
			c.JSON(cpErr.Status, gin.H{"error": cpErr.clientMessage()})
			return
		}
		if decision == nil || !decision.allows(requestPeer) {
			c.JSON(statusForScopeDenial(scope, decision.reason(requestPeer)), gin.H{
				"error": messageForScopeDenial(scope, decision.reason(requestPeer)),
			})
			return
		}
		clientWallet = decision.PrimaryWallet
		// Forward only the key hash to the worker, stripping the client's raw
		// Authorization so the API key never traverses the libp2p hop.
		if bearer, perr := parseBearerToken(c.GetHeader("Authorization")); perr == nil {
			forwardKeyHash = sha256Hex(bearer)
		}
	}
	if clientWallet == "" {
		clientWallet = resolveClientWallet(c)
	}

	target := url.URL{
		Scheme: "libp2p",
		Host:   requestPeer,
		Path:   requestPath,
	}
	transport := getGlobalTransport()
	if scope.trusted() {
		if !protocol.HasTrustedDirectConnection(requestPeer) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no direct trusted path available"})
			return
		}
		transport = getTrustedTransport()
	}

	event := []axiom.Event{{ingest.TimestampField: time.Now(), "event": "P2P Service Forward", "from": &protocol.MyID, "to": requestPeer, "path": requestPath, "service": scope.Service}}
	IngestEvents(event)
	common.Logger.Debugf("P2P service forward: %s", target.String())

	director := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
		// req.URL.Host is what the libp2p RoundTripper dials, so it must be the
		// target peer. Assigning req.Host here instead uses the *incoming* Host
		// header, because req.Host is only set to target.Host on the next line.
		// (ServiceForwardHandler gets this right by assigning req.Host first.)
		req.URL.Host = target.Host
		req.Host = target.Host
		if clientWallet != "" {
			req.Header.Set("X-Otela-Client-Wallet", clientWallet)
		}
		if forwardKeyHash != "" {
			req.Header.Set("X-Otela-Key-Hash", forwardKeyHash)
			req.Header.Del("Authorization")
		}
	}

	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	proxy.Transport = transport
	proxy.ErrorHandler = ErrorHandler
	proxy.ModifyResponse = rewriteHeader()
	proxy.ServeHTTP(c.Writer, c.Request)
}

// ServiceHandler
func ServiceForwardHandler(c *gin.Context) {
	st := newStageTimer()
	serviceName := c.Param("service")
	requestPath := c.Param("path")
	var (
		service protocol.Service
		err     error
	)
	if resolved, ok := c.Get("resolved_local_service"); ok {
		service, ok = resolved.(protocol.Service)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid resolved local service"})
			return
		}
	} else {
		service, err = resolveUniqueLocalService(serviceName)
		if writeServiceResolutionError(c, err) {
			return
		}
	}

	target := url.URL{
		Scheme: "http",
		Host:   service.Host + ":" + service.Port,
		Path:   requestPath,
	}
	director := func(req *http.Request) {
		req.Host = target.Host
		req.URL.Host = req.Host
		req.URL.Scheme = target.Scheme
		req.URL.Path = target.Path
		// The worker has already authorized the request against the control
		// plane (using X-Otela-Key-Hash when forwarded by a trusted head).
		// Drop the client's raw Authorization so it never reaches the local
		// backend (e.g. SGLang), which has no use for it and should not be
		// able to observe or log API keys.
		req.Header.Del("Authorization")
		req.Header.Del("X-Otela-Key-Hash")
	}
	st.Mark("local_proxy")

	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Director = director
	// Use global transport here too if we want pooling to external HTTP services,
	// though standard http.DefaultTransport also pools.
	// However, if we want shared settings (timeouts), we can use ours.
	// NOTE: standard http transport doesn't support libp2p.
	// Ideally we separate p2p transport from standard http transport, OR register protocols on one.
	// Our getGlobalTransport() has registered libp2p, so it works for both (http falls back to standard).
	proxy.Transport = getGlobalTransport()

	proxy.ModifyResponse = func(r *http.Response) error {
		st.Mark("sglang_ttft")
		setWorkerTimingHeader(st, r)
		return nil
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// parseFallbackLevel parses the value of the X-Otela-Fallback header.
// Valid values are 0, 1, or 2; anything else (including an empty string)
// returns 0 (the default, exact-match-only behaviour).
func parseFallbackLevel(header string) int {
	if header == "" {
		return 0
	}
	if lvl, err := strconv.Atoi(header); err == nil && lvl >= 0 && lvl <= 2 {
		return lvl
	}
	return 0
}

// selectCandidates iterates over the provided peers and returns the peer IDs
// that are eligible to serve the named service for the given request body.
//
// Match priority:  exact (3) > wildcard "*" (2) > catch-all "all" (1).
// fallbackLevel controls which tiers are considered when no exact match exists:
//
//	0 – exact matches only
//	1 – exact, then wildcard
//	2 – exact, then wildcard, then catch-all
func selectCandidates(providers []protocol.Peer, serviceName string, body []byte, fallbackLevel int) []string {
	var exactCandidates, wildcardCandidates, catchAllCandidates []string
	for _, provider := range providers {
		for _, service := range provider.Service {
			if service.Name == serviceName {
				// Track the best (highest-priority) match for this provider.
				// 0 = no match, 1 = catch-all, 2 = wildcard, 3 = exact
				bestMatch := 0
				if len(service.IdentityGroup) > 0 {
					for _, ig := range service.IdentityGroup {
						// "all" is a shortcut that matches every request
						if ig == "all" {
							if bestMatch < 1 {
								bestMatch = 1
							}
							continue
						}
						igGroup := strings.SplitN(ig, "=", 2)
						if len(igGroup) != 2 {
							continue
						}
						igKey := igGroup[0]
						igValue := igGroup[1]
						// "*" wildcard: match if the key exists in the request body (any value)
						if igValue == "*" {
							if _, _, _, err := jsonparser.Get(body, igKey); err == nil {
								if bestMatch < 2 {
									bestMatch = 2
								}
							}
							continue
						}
						// exact match
						requestGroup, err := jsonparser.GetString(body, igKey)
						if err == nil && requestGroup == igValue {
							bestMatch = 3
							break // can't do better than exact
						}
					}
				}
				switch bestMatch {
				case 3:
					exactCandidates = append(exactCandidates, provider.ID)
				case 2:
					wildcardCandidates = append(wildcardCandidates, provider.ID)
				case 1:
					catchAllCandidates = append(catchAllCandidates, provider.ID)
				}
				// Once we've recorded a match for this provider, avoid adding it again
				if bestMatch > 0 {
					break
				}
			}
		}
	}

	// Pick from the highest-priority non-empty tier, respecting fallback level
	candidates := exactCandidates
	if len(candidates) == 0 && fallbackLevel >= 1 {
		candidates = wildcardCandidates
	}
	if len(candidates) == 0 && fallbackLevel >= 2 {
		candidates = catchAllCandidates
	}
	return candidates
}

// weightedCandidate pairs a peer ID with a routing score.
type weightedCandidate struct {
	peerID string
	score  float64
}

// weightedRandomSelect picks a peer using weighted-random selection proportional
// to each candidate's score. Falls back to uniform random if all scores are zero.
func weightedRandomSelect(candidates []weightedCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0].peerID
	}

	totalWeight := 0.0
	for _, c := range candidates {
		totalWeight += c.score
	}
	if totalWeight <= 0 {
		return candidates[rand.Intn(len(candidates))].peerID
	}

	r := rand.Float64() * totalWeight
	cumulative := 0.0
	for _, c := range candidates {
		cumulative += c.score
		if r <= cumulative {
			return c.peerID
		}
	}
	return candidates[len(candidates)-1].peerID
}

// scoreCandidates assigns scores to candidate peer IDs. Currently all peers
// receive an equal default score of 1.0; this function is the extension point
// for richer scoring (latency, load, trust, etc.) in the future.
func scoreCandidates(candidateIDs []string) []weightedCandidate {
	result := make([]weightedCandidate, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		score := 1.0 // Default score for non-scalable mode
		result = append(result, weightedCandidate{peerID: id, score: score})
	}
	return result
}

// excludePeers returns the candidates slice with any peer ID present in the
// excluded map removed. It is used to implement retry-with-peer-exclusion so
// that a failed request is not retried against the same worker.
func excludePeers(candidates []string, excluded map[string]bool) []string {
	var result []string
	for _, c := range candidates {
		if !excluded[c] {
			result = append(result, c)
		}
	}
	return result
}

// shouldShedLoad returns true when the head node should reject a request due to
// insufficient worker availability. It uses probabilistic load shedding: the
// acceptance rate is proportional to available/expected workers, so requests
// are rejected with probability (1 - available/expected).
func shouldShedLoad(available, expected int) bool {
	if expected <= 0 || available >= expected {
		return false
	}
	acceptRate := float64(available) / float64(expected)
	return rand.Float64() > acceptRate
}

// filterAllowedCandidatesV2 removes candidate peer IDs whose TrustLevel is
// below minTrust.
func filterByTrust(candidates []string, minTrust int) []string {
	var filtered []string
	for _, id := range candidates {
		peer, err := protocol.GetPeerFromTable(id)
		if err != nil {
			continue
		}
		if peer.TrustLevel >= minTrust {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// intersectAllowedPeers keeps only candidates whose peer ID appears in the
// comma-separated X-Otela-Allowed-Peers header. The billing gate
// (api.opentela.ai) stamps this header with the cheapest affordable peers
// before forwarding; the mesh head intersects it with its identity-group
// candidates so a request is never routed to a peer the buyer cannot afford.
// Order is preserved (the candidates list is already priority-sorted).
func intersectAllowedPeers(candidates []string, allowedHeader string) []string {
	set := make(map[string]bool, 8)
	for _, id := range strings.Split(allowedHeader, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			set[id] = true
		}
	}
	if len(set) == 0 {
		return candidates
	}
	var result []string
	for _, c := range candidates {
		if set[c] {
			result = append(result, c)
		}
	}
	return result
}

// priceAwareScores assigns each remaining candidate a selection weight that
// decays exponentially with its price rank: rank 0 weighs 1, rank r weighs
// decay^r. Peers absent from the order (defensive) weigh like the tail rank.
// The result feeds weightedRandomSelect so the cheapest affordable peer wins
// most traffic while the rest of the affordable set still serves — the market
// signal from the billing gate's cheapest-first X-Otela-Allowed-Peers stamp,
// enabled by routing.price_weight_decay (0 = legacy uniform policies).
func priceAwareScores(ids []string, order []string, decay float64) []weightedCandidate {
	ranks := make(map[string]int, len(order))
	for i, id := range order {
		ranks[id] = i
	}
	if len(ids) == 0 {
		return nil
	}
	tail := len(ids)
	result := make([]weightedCandidate, 0, len(ids))
	for _, id := range ids {
		rank, ok := ranks[id]
		if !ok {
			rank = tail
		}
		result = append(result, weightedCandidate{peerID: id, score: math.Pow(decay, float64(rank))})
	}
	return result
}

// parseAllowedPeerOrder returns the peer IDs in the gate's
// X-Otela-Allowed-Peers header in header order (deduplicated, blanks
// dropped). The billing gate stamps this header cheapest-affordable-first,
// so index 0 is the cheapest peer the buyer can afford.
func parseAllowedPeerOrder(allowedHeader string) []string {
	seen := make(map[string]bool)
	var order []string
	for _, id := range strings.Split(allowedHeader, ",") {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
	}
	return order
}

// orderCandidatesByPrice re-orders candidates to follow the gate's
// cheapest-first price order (stable: peers missing from the order keep
// their relative position at the tail). Combined with the order-preserving
// exclusion in the retry loop, this makes remaining[0] the cheapest
// surviving peer, so a failed attempt deterministically prefers the
// next-cheapest allowed peer instead of a random one.
func orderCandidatesByPrice(candidates []string, order []string) []string {
	ranks := make(map[string]int, len(order))
	for i, id := range order {
		ranks[id] = i
	}
	tail := len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		ri, oki := ranks[candidates[i]]
		if !oki {
			ri = tail
		}
		rj, okj := ranks[candidates[j]]
		if !okj {
			rj = tail
		}
		return ri < rj
	})
	return candidates
}

func filterAllowedCandidatesV2(candidates []string, decision *controlPlaneDecisionV2) []string {
	if decision == nil {
		return candidates
	}
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if decision.allows(candidate) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func buildInternalServicePath(scope routingScope, suffix string) string {
	if scope.Partition == partitionTrustedRegion {
		return "/v1/_regions/" + scope.Region + "/service/" + scope.Service + suffix
	}
	return "/v1/_service/" + scope.Service + suffix
}

// in case of global service, we need to forward the request to the service, identified by the service name and identity group
func GlobalServiceForwardHandler(c *gin.Context) {
	scope, err := newRoutingScope(partitionPermissionless, "", routeKindServiceIngress, c.Param("service"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	globalServiceForwardWithScope(c, scope)
}

func TrustedRegionServiceForwardHandler(c *gin.Context) {
	scope, err := newRoutingScope(partitionTrustedRegion, c.Param("region"), routeKindServiceIngress, c.Param("service"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	globalServiceForwardWithScope(c, scope)
}

func globalServiceForwardWithScope(c *gin.Context, scope routingScope) {
	// Generate request ID for usage tracking
	requestID := usage.GenerateRequestID()
	c.Set("requestId", requestID)

	// Set a longer timeout for AI/ML services
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Minute)
	defer cancel()

	// Always buffer the request body so transport-level failures can retry
	// against a different worker. The retry loop replays bodyBytes on each
	// attempt.
	st := newStageTimer()
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	st.Mark("head_recv")
	c.Request = c.Request.WithContext(ctx)

	// matchBody is used *only* by selectCandidates to pick the identity group.
	// If the caller provides X-Otela-Identity-Group we synthesize a tiny JSON
	// object ({"key":"value"}) so the matcher works unchanged, without needing
	// to parse the real body for routing. The actual body forwarded to the
	// worker is always bodyBytes.
	matchBody := bodyBytes
	if identityGroupHeader := c.GetHeader("X-Otela-Identity-Group"); identityGroupHeader != "" {
		parts := strings.SplitN(identityGroupHeader, "=", 2)
		if len(parts) == 2 {
			obj := map[string]string{parts[0]: parts[1]}
			if b, err := json.Marshal(obj); err == nil {
				matchBody = b
			}
		}
	}

	serviceName := scope.Service
	routingStart := time.Now()
	requestPath := c.Param("path")
	providers, err := protocol.GetAllProviders(serviceName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Determine fallback level from the X-Otela-Fallback request header.
	// 0 (default): exact match only
	// 1: allow wildcard fallback when no exact match exists
	// 2: allow wildcard + catch-all fallback
	fallbackLevel := parseFallbackLevel(c.GetHeader("X-Otela-Fallback"))

	candidates := selectCandidates(providers, serviceName, matchBody, fallbackLevel)
	st.Mark("head_dnt")
	routingFallbackTotal.WithLabelValues(serviceName, strconv.Itoa(fallbackLevel)).Inc()

	// Price order from the billing gate (empty when the header is absent).
	var allowedPeerOrder []string

	// X-Otela-Allowed-Peers: the billing gate (api.opentela.ai) stamps the
	// cheapest affordable peers on every forwarded request. Intersect the
	// identity-group candidates with this allowlist so the mesh head never
	// routes to a peer the buyer cannot afford. An absent header means no
	// billing constraint (backward compatible with pre-billing traffic).
	if allowed := c.GetHeader("X-Otela-Allowed-Peers"); allowed != "" {
		candidates = intersectAllowedPeers(candidates, allowed)
		if len(candidates) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no billing-affordable provider for the requested service."})
			return
		}
		// The gate stamps this header cheapest-affordable-first; keep the
		// order for price-aware routing (routing.price_weight_decay).
		allowedPeerOrder = parseAllowedPeerOrder(allowed)
	}

	if len(candidates) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No provider found for the requested service."})
		return
	}

	clientWallet := ""
	forwardKeyHash := ""
	if isControlPlaneEnabled() {
		decision, cpErr := authorizeRequestForScope(ctx, c.GetHeader("Authorization"), scope, candidates, "")
		if cpErr != nil {
			c.JSON(cpErr.Status, gin.H{"error": cpErr.clientMessage()})
			return
		}
		candidates = filterAllowedCandidatesV2(candidates, decision)
		if len(candidates) == 0 {
			c.JSON(statusForScopeDenial(scope, ""), gin.H{"error": "access denied by instance ACL"})
			return
		}
		clientWallet = decision.PrimaryWallet
		// The head has validated the bearer. Forward only its hash to workers
		// (X-Otela-Key-Hash) and strip the raw Authorization so the client's
		// API key never traverses the libp2p hop or reaches a local backend.
		if bearer, perr := parseBearerToken(c.GetHeader("Authorization")); perr == nil {
			forwardKeyHash = sha256Hex(bearer)
		}
	}

	if scope.trusted() {
		directCandidates := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			if protocol.HasTrustedDirectConnection(candidate) {
				directCandidates = append(directCandidates, candidate)
			}
		}
		if len(directCandidates) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no direct trusted providers available"})
			return
		}
		candidates = directCandidates
	}

	// Trust-aware filtering: if the client specifies a minimum trust level
	// via X-Otela-Trust, remove candidates that don't meet the threshold.
	if scope.Partition == partitionPermissionless {
		if trustHeader := c.GetHeader("X-Otela-Trust"); trustHeader != "" {
			if minTrust, err := strconv.Atoi(trustHeader); err == nil && minTrust > 0 {
				candidates = filterByTrust(candidates, minTrust)
				if len(candidates) == 0 {
					c.JSON(http.StatusServiceUnavailable, gin.H{
						"error": fmt.Sprintf("No provider meets the requested trust level (%d).", minTrust),
					})
					return
				}
			}
		}
	}

	// Admission control: probabilistically reject requests when the number of
	// available workers is below the configured expected count.
	if viper.GetBool("scalability.admission_control") {
		expected := viper.GetInt("scalability.expected_workers")
		if expected > 0 && shouldShedLoad(len(candidates), expected) {
			c.Header("Retry-After", "5")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service degraded, try again later"})
			return
		}
	}

	// --- Retry loop: peer selection + proxy forwarding ---

	maxRetries := 0
	if viper.GetBool("routing.retry_enabled") {
		maxRetries = viper.GetInt("routing.max_retries")
	}
	maxBuffer := viper.GetInt("routing.max_response_buffer_bytes")
	excluded := make(map[string]bool)

	// Streaming detection — layer 1: request body "stream":true (OpenAI-compatible)
	// Layer 2: Accept header. Layer 3 is in retryableResponseWriter.WriteHeader.
	streamVal, streamType, _, _ := jsonparser.Get(bodyBytes, "stream")
	isStreaming := (streamType == jsonparser.Boolean && string(streamVal) == "true") ||
		strings.Contains(c.GetHeader("Accept"), "text/event-stream")

	requestPath = buildInternalServicePath(scope, requestPath)
	if clientWallet == "" {
		clientWallet = resolveClientWallet(c)
	}

	// Resolve the model dimension once for analytics (OpenAI body field, with
	// identity-group header fallback). Empty when analytics is disabled.
	analyticsModel := ""
	if getAnalytics() != nil {
		if mv, _, _, mErr := jsonparser.Get(bodyBytes, "model"); mErr == nil {
			analyticsModel = string(mv)
		}
		if analyticsModel == "" {
			if ig := c.GetHeader("X-Otela-Identity-Group"); strings.HasPrefix(ig, "model=") {
				analyticsModel = strings.TrimPrefix(ig, "model=")
			}
		}
	}

	// Load balancer configured via the lb-policy flag (random, round-robin,
	// shortest-queue). Used for peer selection on the non-weighted path and to
	// track in-flight requests per peer for queue-aware policies.
	lb := GetLoadBalancer()

	// Price-aware routing (the mesh-head half of the API market): when
	// configured, bias selection toward the cheapest affordable peers. The
	// billing gate stamps X-Otela-Allowed-Peers cheapest-first; a decay in
	// (0,1) turns that order into exponentially-decaying selection weights so
	// the cheapest peer wins most traffic while the rest of the affordable
	// set still serves. decay 0 (default) keeps the legacy uniform policies.
	priceDecay := viper.GetFloat64("routing.price_weight_decay")
	priceAware := priceDecay > 0 && priceDecay < 1 && len(allowedPeerOrder) > 0
	if priceAware {
		// Price-order candidates up front: with the order-preserving retry
		// exclusion below, remaining[0] is then always the cheapest
		// surviving affordable peer.
		candidates = orderCandidatesByPrice(candidates, allowedPeerOrder)
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		remaining := excludePeers(candidates, excluded)
		if len(remaining) == 0 {
			break
		}

		// Select peer
		var targetPeer string
		switch {
		case priceAware && attempt == 0:
			// First pick: market signal — weight by price rank (cheapest
			// affordable first) so load still spreads across the affordable
			// set.
			targetPeer = weightedRandomSelect(priceAwareScores(remaining, allowedPeerOrder, priceDecay))
		case priceAware:
			// Retry after a failure: deterministically prefer the
			// next-cheapest surviving allowed peer.
			targetPeer = remaining[0]
		case viper.GetBool("scalability.weighted_routing"):
			weighted := scoreCandidates(remaining)
			targetPeer = weightedRandomSelect(weighted)
		default:
			// Pick among the remaining candidates using the configured
			// load balancing policy.
			targetPeer = remaining[lb.Pick(remaining)]
		}
		excluded[targetPeer] = true
		if attempt == 0 {
			st.Mark("head_peer_select")
		}

		// Clone request — ReverseProxy mutates URL, Host, X-Forwarded-*
		attemptReq := c.Request.Clone(ctx)
		attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		attemptReq.Header.Set("X-Otela-Request-Id", requestID)

		event := []axiom.Event{{ingest.TimestampField: time.Now(), "event": "Service Forward", "from": protocol.MyID, "to": targetPeer, "path": requestPath, "service": serviceName}}
		IngestEvents(event)
		common.Logger.Debugf("Service forward: attempt=%d peer=%s path=%s", attempt+1, targetPeer, requestPath)

		// Resolve target URL (direct or via relay)
		var target url.URL
		transport := getGlobalTransport()
		if scope.trusted() {
			if !protocol.HasTrustedDirectConnection(targetPeer) {
				if attempt <= maxRetries {
					routingRetriesTotal.WithLabelValues(serviceName, strconv.Itoa(attempt+1)).Inc()
				}
				continue
			}
			target = url.URL{Scheme: "libp2p", Host: targetPeer, Path: requestPath}
			transport = getTrustedTransport()
		} else if protocol.IsDirectlyConnected(targetPeer) {
			target = url.URL{Scheme: "libp2p", Host: targetPeer, Path: requestPath}
		} else {
			relayPeer := protocol.FindRelayFor(targetPeer)
			if relayPeer == "" {
				common.Logger.Warnf("No relay for peer %s, skipping", targetPeer)
				if attempt <= maxRetries {
					routingRetriesTotal.WithLabelValues(serviceName, strconv.Itoa(attempt+1)).Inc()
				}
				continue
			}
			relayPath := "/v1/p2p/" + targetPeer + requestPath
			common.Logger.Debugf("Relay hop: relay=%s path=%s", relayPeer[:12], relayPath)
			target = url.URL{Scheme: "libp2p", Host: relayPeer, Path: relayPath}
		}

		// Wrap c.Writer with a first-byte marker so we can measure the head's
		// response-startup cost (time from ModifyResponse → first body byte on
		// the wire to client). The marker fires inside retryableResponseWriter's
		// passthrough Write path, so it captures the real first-write moment.
		fbw := newFirstByteMarker(c.Writer, st, "head_first_byte_sent")
		rw := newRetryableResponseWriter(fbw, isStreaming, maxBuffer)

		director := func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Path = target.Path
			req.URL.Host = target.Host
			req.Host = target.Host
			if clientWallet != "" {
				req.Header.Set("X-Otela-Client-Wallet", clientWallet)
			}
			if forwardKeyHash != "" {
				req.Header.Set("X-Otela-Key-Hash", forwardKeyHash)
				req.Header.Del("Authorization")
			}
		}

		proxy := httputil.NewSingleHostReverseProxy(&target)
		proxy.Director = director
		proxy.Transport = transport
		proxy.ErrorHandler = func(res http.ResponseWriter, req *http.Request, err error) {
			if rrw, ok := res.(*retryableResponseWriter); ok {
				common.Logger.Warnf("Transport error for peer %s: %v", targetPeer, err)
				rrw.markFailed()
				return
			}
			if _, werr := fmt.Fprintf(res, "ERROR: %s", err.Error()); werr != nil {
				common.Logger.Debug("Error writing error response: ", werr)
			}
		}

		capturedPeer := targetPeer
		proxy.ModifyResponse = func(r *http.Response) error {
			st.Mark("head_p2p_to_worker_first_byte")
			mergeWorkerTiming(st, r)
			if hv := st.Header(); hv != "" {
				r.Header.Set("Server-Timing", hv)
			}
			if err := rewriteHeader()(r); err != nil {
				return err
			}
			r.Header.Set("X-Computing-Node", capturedPeer)
			if viper.GetBool("billing.enabled") {
				if metrics, err := usage.ExtractUsageMetrics(r); err == nil && len(metrics) > 0 {
					for metricName, value := range metrics {
						if err := usage.Track(requestID, serviceName, protocol.MyID, capturedPeer, metricName, value); err != nil {
							common.Logger.Errorf("Tracking usage: %v", err)
						}
					}
				}
			}
			return nil
		}

		// Track the in-flight request for queue-aware policies. ServeHTTP is
		// synchronous (blocks until the response, including streaming, is
		// complete), so the peer is considered busy for its whole duration.
		conc := getAnalytics().Begin(serviceName, analyticsModel, targetPeer)
		lb.OnRequestStart(targetPeer)
		proxy.ServeHTTP(rw, attemptReq)
		lb.OnRequestEnd(targetPeer)
		getAnalytics().End(serviceName, analyticsModel, targetPeer)

		// Surface the measured "head response startup" duration to the client
		// as an SSE-comment line appended after the worker's body. Trailers
		// would be the HTTP-idiomatic choice but Python HTTP clients (httpx,
		// aiohttp ≤ 3.13) don't expose trailers, so we use a comment line
		// (lines starting with `:` are no-ops in SSE consumers) carrying the
		// timing for bench-side parsing. Streamed responses only.
		if isStreaming && rw.headersSent {
			if d, ok := fbw.Duration(); ok {
				fmt.Fprintf(c.Writer, ": otela-head-first-byte=%.3f\n\n", d)
				if f, ok := c.Writer.(http.Flusher); ok {
					f.Flush()
				}
			}
		}

		if rw.isRetryable() {
			routingRetriesTotal.WithLabelValues(serviceName, strconv.Itoa(attempt+1)).Inc()
			common.Logger.Warnf("Attempt %d/%d failed for peer %s, retrying",
				attempt+1, maxRetries+1, targetPeer)
			continue
		}

		// Success (or streaming already committed to client)
		if !rw.headersSent {
			rw.flushToClient()
		}
		if attempt > 0 {
			routingRetriesTotal.WithLabelValues(serviceName, "succeeded_after_retry").Inc()
		}
		status := strconv.Itoa(c.Writer.Status())
		routingRequestsTotal.WithLabelValues(serviceName, status).Inc()
		routingRequestDuration.WithLabelValues(serviceName).Observe(time.Since(routingStart).Seconds())
		if rec := getAnalytics(); rec != nil {
			rec.Observe(buildProxySample(serviceName, analyticsModel, targetPeer, rw, st, conc, time.Since(routingStart)))
		}
		return
	}

	// All attempts exhausted (or no candidates left)
	routingRetriesTotal.WithLabelValues(serviceName, "exhausted").Inc()
	routingRequestsTotal.WithLabelValues(serviceName, "502").Inc()
	routingRequestDuration.WithLabelValues(serviceName).Observe(time.Since(routingStart).Seconds())
	c.JSON(http.StatusBadGateway, gin.H{"error": "all workers unreachable for service " + serviceName})
}
