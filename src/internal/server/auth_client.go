package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"opentela/internal/common"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const (
	controlPlaneBatchLimit     = 100
	controlPlaneResponseMaxLen = 256 << 10
	controlAuthFailureHeader   = "X-Otela-Control-Auth-Failed"
)

var (
	// legacyAuthHTTPClient is retained for the deprecated auth_url fallback
	// when security.control_plane.url is not configured.
	legacyAuthHTTPClient   = &http.Client{Timeout: 5 * time.Second}
	controlPlaneHTTPClient = &http.Client{}

	errMissingBearer       = errors.New("missing bearer token")
	errInvalidBearer       = errors.New("invalid bearer token")
	errInvalidToken        = errors.New("invalid or revoked token")
	errEvaluatorDown       = errors.New("control-plane evaluator unavailable")
	errMalformedResponse   = errors.New("invalid control-plane response")
	errMissingControlToken = errors.New("missing control-plane token")
)

type authVerifyResponse struct {
	Wallet string `json:"wallet"`
	KeyID  string `json:"key_id"`
}

type aclEvaluateRequest struct {
	KeyHash string   `json:"key_hash"`
	PeerIDs []string `json:"peer_ids"`
}

type aclEvaluateV2Request struct {
	KeyHash        string           `json:"key_hash"`
	Partition      routingPartition `json:"partition"`
	Region         string           `json:"region"`
	RouteKind      routeKind        `json:"route_kind"`
	Service        string           `json:"service"`
	PeerIDs        []string         `json:"peer_ids"`
	UpstreamPeerID string           `json:"upstream_peer_id,omitempty"`
}

type aclDeniedPeer struct {
	PeerID string `json:"peer_id"`
	Reason string `json:"reason"`
}

type aclEvaluateResponse struct {
	KeyID           string          `json:"key_id"`
	AllowedPeerIDs  []string        `json:"allowed_peer_ids"`
	Denied          []aclDeniedPeer `json:"denied"`
	PrimaryWallet   string          `json:"primary_wallet"`
	CacheTTLSeconds int             `json:"cache_ttl_seconds"`
}

type aclDecisionScope struct {
	Partition      routingPartition `json:"partition"`
	Region         string           `json:"region"`
	RouteKind      routeKind        `json:"route_kind"`
	Service        string           `json:"service"`
	RegionRevision int64            `json:"region_revision"`
}

type aclDecisionV2 struct {
	PeerID                string    `json:"peer_id"`
	Allowed               bool      `json:"allowed"`
	Reason                string    `json:"reason"`
	PolicyScope           string    `json:"policy_scope"`
	ServiceExposure       string    `json:"service_exposure"`
	PolicyRevision        int64     `json:"policy_revision"`
	ServicePolicyRevision int64     `json:"service_policy_revision"`
	MembershipRevision    int64     `json:"membership_revision"`
	EvaluatedAt           time.Time `json:"evaluated_at"`
}

type aclEvaluateV2Response struct {
	KeyID           string           `json:"key_id"`
	PrimaryWallet   string           `json:"primary_wallet"`
	DecisionScope   aclDecisionScope `json:"decision_scope"`
	Decisions       []aclDecisionV2  `json:"decisions"`
	CacheTTLSeconds int              `json:"cache_ttl_seconds"`
}

type controlPlaneDecision struct {
	KeyID          string
	PrimaryWallet  string
	AllowedPeerIDs []string
	DeniedReasons  map[string]string
}

type controlPlaneDecisionV2 struct {
	KeyID         string
	PrimaryWallet string
	Scope         aclDecisionScope
	Decisions     map[string]aclDecisionV2
}

func (d *controlPlaneDecision) allows(peerID string) bool {
	return slices.Contains(d.AllowedPeerIDs, peerID)
}

func (d *controlPlaneDecisionV2) allows(peerID string) bool {
	if d == nil {
		return false
	}
	decision, ok := d.Decisions[peerID]
	return ok && decision.Allowed
}

func (d *controlPlaneDecisionV2) reason(peerID string) string {
	if d == nil {
		return ""
	}
	decision, ok := d.Decisions[peerID]
	if !ok {
		return ""
	}
	return decision.Reason
}

type controlPlaneError struct {
	Status int
	Err    error
}

func (e *controlPlaneError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *controlPlaneError) clientMessage() string {
	if e == nil {
		return "control-plane evaluator unavailable"
	}
	switch e.Status {
	case http.StatusUnauthorized:
		if errors.Is(e.Err, errMissingBearer) || errors.Is(e.Err, errInvalidBearer) {
			return "invalid or missing bearer token"
		}
		return "invalid or revoked token"
	default:
		return "control-plane evaluator unavailable"
	}
}

type decisionCache struct {
	mu      sync.RWMutex
	entries map[string]decisionCacheEntry
}

type decisionCacheEntry struct {
	decision   controlPlaneDecision
	freshUntil time.Time
	staleUntil time.Time
}

type decisionCacheV2 struct {
	mu      sync.RWMutex
	entries map[string]decisionCacheEntryV2
}

type decisionCacheEntryV2 struct {
	decision   controlPlaneDecisionV2
	freshUntil time.Time
	staleUntil time.Time
}

var (
	tokenCache         = &authCache{entries: make(map[string]authCacheEntry)}
	aclDecisionCache   = &decisionCache{entries: make(map[string]decisionCacheEntry)}
	aclDecisionCacheV2 = &decisionCacheV2{entries: make(map[string]decisionCacheEntryV2)}
)

// authCache caches token → wallet mappings to avoid hitting the legacy auth
// server on every request. Entries expire after 60 seconds.
type authCache struct {
	mu      sync.RWMutex
	entries map[string]authCacheEntry
}

type authCacheEntry struct {
	wallet  string
	expires time.Time
}

func (c *authCache) get(token string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[token]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.wallet, true
}

func (c *authCache) set(token, wallet string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[token] = authCacheEntry{
		wallet:  wallet,
		expires: time.Now().Add(60 * time.Second),
	}
}

func (c *decisionCache) getFresh(key string, now time.Time) (*controlPlaneDecision, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.freshUntil) {
		return nil, false
	}
	decision := entry.decision
	return &decision, true
}

func (c *decisionCache) getStale(key string, now time.Time) (*controlPlaneDecision, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.staleUntil) {
		return nil, false
	}
	decision := entry.decision
	return &decision, true
}

func (c *decisionCache) set(key string, decision controlPlaneDecision, freshFor, staleFor time.Duration, now time.Time) {
	freshUntil := now.Add(freshFor)
	staleUntil := freshUntil
	if staleFor > 0 {
		staleUntil = staleUntil.Add(staleFor)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = decisionCacheEntry{
		decision:   decision,
		freshUntil: freshUntil,
		staleUntil: staleUntil,
	}
}

func (c *decisionCacheV2) getFresh(key string, now time.Time) (*controlPlaneDecisionV2, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.freshUntil) {
		return nil, false
	}
	decision := entry.decision
	return &decision, true
}

func (c *decisionCacheV2) getStale(key string, now time.Time) (*controlPlaneDecisionV2, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.staleUntil) {
		return nil, false
	}
	decision := entry.decision
	return &decision, true
}

func (c *decisionCacheV2) set(key string, decision controlPlaneDecisionV2, freshFor, staleFor time.Duration, now time.Time) {
	freshUntil := now.Add(freshFor)
	staleUntil := freshUntil
	if staleFor > 0 {
		staleUntil = staleUntil.Add(staleFor)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = decisionCacheEntryV2{
		decision:   decision,
		freshUntil: freshUntil,
		staleUntil: staleUntil,
	}
}

func isControlPlaneEnabled() bool {
	return strings.TrimSpace(viper.GetString("security.control_plane.url")) != ""
}

func controlPlaneTimeout() time.Duration {
	if d := viper.GetDuration("security.control_plane.timeout"); d > 0 {
		return d
	}
	return 5 * time.Second
}

func controlPlaneCacheTTL() time.Duration {
	if d := viper.GetDuration("security.control_plane.cache_ttl"); d > 0 {
		return d
	}
	return 60 * time.Second
}

func controlPlaneStaleIfError() time.Duration {
	if d := viper.GetDuration("security.control_plane.stale_if_error"); d > 0 {
		return d
	}
	return 2 * time.Minute
}

func parseBearerToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errMissingBearer
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errInvalidBearer
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errInvalidBearer
	}
	return token, nil
}

func uniquePeerIDs(peerIDs []string) []string {
	seen := make(map[string]struct{}, len(peerIDs))
	unique := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			continue
		}
		if _, ok := seen[peerID]; ok {
			continue
		}
		seen[peerID] = struct{}{}
		unique = append(unique, peerID)
	}
	return unique
}

func cacheKeyForDecision(keyHash string, peerIDs []string) string {
	sortedPeerIDs := append([]string(nil), peerIDs...)
	sort.Strings(sortedPeerIDs)
	return keyHash + "\x00" + strings.Join(sortedPeerIDs, "\x00")
}

func cacheKeyForDecisionV2(keyHash string, scope routingScope, peerIDs []string) string {
	sortedPeerIDs := append([]string(nil), peerIDs...)
	sort.Strings(sortedPeerIDs)
	return strings.Join([]string{
		keyHash,
		"v2",
		string(scope.Partition),
		scope.Region,
		string(scope.RouteKind),
		scope.Service,
		strings.Join(sortedPeerIDs, "\x00"),
	}, "\x00")
}

func sha256Hex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum[:])
}

func controlPlaneFreshTTL(serverTTLSeconds int) time.Duration {
	localCap := controlPlaneCacheTTL()
	if serverTTLSeconds <= 0 {
		return localCap
	}
	serverTTL := time.Duration(serverTTLSeconds) * time.Second
	if localCap <= 0 || serverTTL < localCap {
		return serverTTL
	}
	return localCap
}

func decodeStrictJSON(r io.Reader, target any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func allowedPeerSet(peerIDs []string) map[string]struct{} {
	result := make(map[string]struct{}, len(peerIDs))
	for _, peerID := range peerIDs {
		result[peerID] = struct{}{}
	}
	return result
}

func validateACLResponse(resp aclEvaluateResponse, requestedPeerIDs []string) (controlPlaneDecision, error) {
	if resp.KeyID == "" {
		return controlPlaneDecision{}, fmt.Errorf("%w: missing key_id", errMalformedResponse)
	}

	requested := allowedPeerSet(requestedPeerIDs)
	seen := make(map[string]struct{}, len(requestedPeerIDs))
	allowed := make([]string, 0, len(resp.AllowedPeerIDs))
	for _, peerID := range resp.AllowedPeerIDs {
		if _, ok := requested[peerID]; !ok {
			return controlPlaneDecision{}, fmt.Errorf("%w: unknown allowed peer %q", errMalformedResponse, peerID)
		}
		if _, ok := seen[peerID]; ok {
			return controlPlaneDecision{}, fmt.Errorf("%w: duplicate peer %q", errMalformedResponse, peerID)
		}
		seen[peerID] = struct{}{}
		allowed = append(allowed, peerID)
	}

	denied := make(map[string]string, len(resp.Denied))
	for _, entry := range resp.Denied {
		if _, ok := requested[entry.PeerID]; !ok {
			return controlPlaneDecision{}, fmt.Errorf("%w: unknown denied peer %q", errMalformedResponse, entry.PeerID)
		}
		if _, ok := seen[entry.PeerID]; ok {
			return controlPlaneDecision{}, fmt.Errorf("%w: duplicate peer %q", errMalformedResponse, entry.PeerID)
		}
		if !isKnownACLReason(entry.Reason) {
			return controlPlaneDecision{}, fmt.Errorf("%w: unknown deny reason %q", errMalformedResponse, entry.Reason)
		}
		seen[entry.PeerID] = struct{}{}
		denied[entry.PeerID] = entry.Reason
	}

	if len(seen) != len(requested) {
		return controlPlaneDecision{}, fmt.Errorf("%w: incomplete peer classification", errMalformedResponse)
	}

	return controlPlaneDecision{
		KeyID:          resp.KeyID,
		PrimaryWallet:  resp.PrimaryWallet,
		AllowedPeerIDs: allowed,
		DeniedReasons:  denied,
	}, nil
}

func isKnownACLReason(reason string) bool {
	switch reason {
	case "unmanaged", "public", "owner", "email_domain", "wallet",
		"no_match", "stale_identity", "ownership_mismatch", "ownership_unavailable", "service_context_required":
		return true
	default:
		return false
	}
}

func isKnownACLReasonV2(reason string) bool {
	switch reason {
	// "api_key" is reserved for the control plane's consumer-key rule matches
	// (model sharing via ACL): ship this whitelist fleet-wide BEFORE the API
	// starts emitting it — an unlisted reason fails the whole evaluator
	// response, and old cores must not reject new allow reasons.
	// "no_match" is the control plane's ACL deny verdict; v2 validates every
	// decision's reason, so it must be listed alongside the allow reasons.
	case "instance_acl_allow", "api_key", "no_match",
		"public", "owner", "email_domain", "wallet",
		"unmanaged", "not_region_member", "membership_suspended", "membership_expired",
		"membership_revoked", "ownership_mismatch", "ownership_unavailable",
		"partition_mismatch", "trusted_route_required", "untrusted_upstream",
		"wrong_role", "service_context_required", "service_undeclared",
		"duplicate_service_name", "service_disabled":
		return true
	default:
		return false
	}
}

func validateACLResponseV2(resp aclEvaluateV2Response, request aclEvaluateV2Request) (controlPlaneDecisionV2, error) {
	if resp.KeyID == "" {
		return controlPlaneDecisionV2{}, fmt.Errorf("%w: missing key_id", errMalformedResponse)
	}
	if resp.DecisionScope.Partition != request.Partition ||
		resp.DecisionScope.Region != request.Region ||
		resp.DecisionScope.RouteKind != request.RouteKind ||
		resp.DecisionScope.Service != request.Service {
		return controlPlaneDecisionV2{}, fmt.Errorf("%w: decision_scope mismatch", errMalformedResponse)
	}
	requested := allowedPeerSet(request.PeerIDs)
	decisions := make(map[string]aclDecisionV2, len(request.PeerIDs))
	for _, decision := range resp.Decisions {
		if _, ok := requested[decision.PeerID]; !ok {
			return controlPlaneDecisionV2{}, fmt.Errorf("%w: unknown peer %q", errMalformedResponse, decision.PeerID)
		}
		if _, ok := decisions[decision.PeerID]; ok {
			return controlPlaneDecisionV2{}, fmt.Errorf("%w: duplicate peer %q", errMalformedResponse, decision.PeerID)
		}
		if !isKnownACLReasonV2(decision.Reason) {
			return controlPlaneDecisionV2{}, fmt.Errorf("%w: unknown reason %q", errMalformedResponse, decision.Reason)
		}
		switch decision.PolicyScope {
		case "", "peer", "service":
		default:
			return controlPlaneDecisionV2{}, fmt.Errorf("%w: unknown policy_scope %q", errMalformedResponse, decision.PolicyScope)
		}
		switch decision.ServiceExposure {
		case "", "permissionless", "trusted_region", "disabled":
		default:
			return controlPlaneDecisionV2{}, fmt.Errorf("%w: unknown service_exposure %q", errMalformedResponse, decision.ServiceExposure)
		}
		decisions[decision.PeerID] = decision
	}
	if len(decisions) != len(requested) {
		return controlPlaneDecisionV2{}, fmt.Errorf("%w: incomplete peer classification", errMalformedResponse)
	}
	return controlPlaneDecisionV2{
		KeyID:         resp.KeyID,
		PrimaryWallet: resp.PrimaryWallet,
		Scope:         resp.DecisionScope,
		Decisions:     decisions,
	}, nil
}

func shouldCacheDecisionV2(scope routingScope) bool {
	return scope.Partition == partitionPermissionless && scope.RouteKind != routeKindWorker
}

func evaluateBearerForPeers(ctx context.Context, bearer string, peerIDs []string) (*controlPlaneDecision, *controlPlaneError) {
	uniquePeers := uniquePeerIDs(peerIDs)
	if len(uniquePeers) == 0 {
		return &controlPlaneDecision{DeniedReasons: map[string]string{}}, nil
	}
	if len(uniquePeers) > controlPlaneBatchLimit {
		combined := &controlPlaneDecision{DeniedReasons: make(map[string]string, len(uniquePeers))}
		keyIDSet := false
		primaryWalletSet := false
		for start := 0; start < len(uniquePeers); start += controlPlaneBatchLimit {
			end := min(start+controlPlaneBatchLimit, len(uniquePeers))
			decision, cpErr := evaluateBearerForPeers(ctx, bearer, uniquePeers[start:end])
			if cpErr != nil {
				return nil, cpErr
			}
			if !keyIDSet {
				combined.KeyID = decision.KeyID
				keyIDSet = true
			} else if decision.KeyID != combined.KeyID {
				return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errMalformedResponse}
			}
			if !primaryWalletSet {
				combined.PrimaryWallet = decision.PrimaryWallet
				primaryWalletSet = true
			} else if decision.PrimaryWallet != combined.PrimaryWallet {
				return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errMalformedResponse}
			}
			combined.AllowedPeerIDs = append(combined.AllowedPeerIDs, decision.AllowedPeerIDs...)
			for peerID, reason := range decision.DeniedReasons {
				combined.DeniedReasons[peerID] = reason
			}
		}
		return combined, nil
	}
	controlToken := strings.TrimSpace(viper.GetString("security.control_plane.token"))
	if controlToken == "" {
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errMissingControlToken}
	}

	keyHash := sha256Hex(bearer)
	cacheKey := cacheKeyForDecision(keyHash, uniquePeers)
	now := time.Now()
	if decision, ok := aclDecisionCache.getFresh(cacheKey, now); ok {
		return decision, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, controlPlaneTimeout())
	defer cancel()

	controlURL := strings.TrimRight(viper.GetString("security.control_plane.url"), "/") + "/internal/acl/evaluate"
	payload := aclEvaluateRequest{KeyHash: keyHash, PeerIDs: uniquePeers}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, controlURL, bytes.NewReader(body))
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+controlToken)

	resp, err := controlPlaneHTTPClient.Do(req)
	if err != nil {
		if decision, ok := aclDecisionCache.getStale(cacheKey, now); ok {
			return decision, nil
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if strings.EqualFold(strings.TrimSpace(resp.Header.Get(controlAuthFailureHeader)), "true") {
			if decision, ok := aclDecisionCache.getStale(cacheKey, now); ok {
				return decision, nil
			}
			return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
		}
		return nil, &controlPlaneError{Status: http.StatusUnauthorized, Err: errInvalidToken}
	}
	if resp.StatusCode != http.StatusOK {
		if decision, ok := aclDecisionCache.getStale(cacheKey, now); ok {
			return decision, nil
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}

	limitedBody := io.LimitReader(resp.Body, controlPlaneResponseMaxLen)
	var payloadResp aclEvaluateResponse
	if err := decodeStrictJSON(limitedBody, &payloadResp); err != nil {
		if decision, ok := aclDecisionCache.getStale(cacheKey, now); ok {
			return decision, nil
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("%w: %v", errMalformedResponse, err)}
	}

	decision, err := validateACLResponse(payloadResp, uniquePeers)
	if err != nil {
		if stale, ok := aclDecisionCache.getStale(cacheKey, now); ok {
			return stale, nil
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: err}
	}

	aclDecisionCache.set(cacheKey, decision, controlPlaneFreshTTL(payloadResp.CacheTTLSeconds), controlPlaneStaleIfError(), now)
	return &decision, nil
}

func evaluateBearerForScope(ctx context.Context, bearer string, scope routingScope, peerIDs []string, upstreamPeerID string) (*controlPlaneDecisionV2, *controlPlaneError) {
	return evaluateKeyHashForScope(ctx, sha256Hex(bearer), scope, peerIDs, upstreamPeerID)
}

// evaluateKeyHashForScope is the key-hash-aware core of evaluateBearerForScope.
// A trusted head forwards the caller's key hash via X-Otela-Key-Hash instead
// of the raw bearer, so the client's API key never has to reach the worker.
func evaluateKeyHashForScope(ctx context.Context, keyHash string, scope routingScope, peerIDs []string, upstreamPeerID string) (*controlPlaneDecisionV2, *controlPlaneError) {
	uniquePeers := uniquePeerIDs(peerIDs)
	if len(uniquePeers) == 0 {
		return &controlPlaneDecisionV2{
			Scope:     aclDecisionScope{Partition: scope.Partition, Region: scope.Region, RouteKind: scope.RouteKind, Service: scope.Service},
			Decisions: map[string]aclDecisionV2{},
		}, nil
	}

	now := time.Now()
	cacheKey := cacheKeyForDecisionV2(keyHash, scope, uniquePeers)
	useCache := shouldCacheDecisionV2(scope)
	if useCache {
		if decision, ok := aclDecisionCacheV2.getFresh(cacheKey, now); ok {
			return decision, nil
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, controlPlaneTimeout())
	defer cancel()

	payload := aclEvaluateV2Request{
		KeyHash:        keyHash,
		Partition:      scope.Partition,
		Region:         scope.Region,
		RouteKind:      scope.RouteKind,
		Service:        scope.Service,
		PeerIDs:        uniquePeers,
		UpstreamPeerID: upstreamPeerID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}

	controlURL := strings.TrimRight(viper.GetString("security.control_plane.url"), "/") + "/internal/acl/evaluate-v2"
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, controlURL, bytes.NewReader(body))
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}
	req.Header.Set("Content-Type", "application/json")

	if scope.Partition == partitionTrustedRegion {
		token, err := getTrustedNodeCredential(timeoutCtx)
		if err != nil {
			return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		controlToken := strings.TrimSpace(viper.GetString("security.control_plane.token"))
		if controlToken == "" {
			return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errMissingControlToken}
		}
		req.Header.Set("Authorization", "Bearer "+controlToken)
	}

	resp, err := controlPlaneHTTPClient.Do(req)
	if err != nil {
		if useCache {
			if decision, ok := aclDecisionCacheV2.getStale(cacheKey, now); ok {
				return decision, nil
			}
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if strings.EqualFold(strings.TrimSpace(resp.Header.Get(controlAuthFailureHeader)), "true") {
			if useCache {
				if decision, ok := aclDecisionCacheV2.getStale(cacheKey, now); ok {
					return decision, nil
				}
			}
			return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
		}
		return nil, &controlPlaneError{Status: http.StatusUnauthorized, Err: errInvalidToken}
	}
	if resp.StatusCode != http.StatusOK {
		if useCache {
			if decision, ok := aclDecisionCacheV2.getStale(cacheKey, now); ok {
				return decision, nil
			}
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: errEvaluatorDown}
	}

	var payloadResp aclEvaluateV2Response
	if err := decodeStrictJSON(io.LimitReader(resp.Body, controlPlaneResponseMaxLen), &payloadResp); err != nil {
		if useCache {
			if decision, ok := aclDecisionCacheV2.getStale(cacheKey, now); ok {
				return decision, nil
			}
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("%w: %v", errMalformedResponse, err)}
	}

	decision, err := validateACLResponseV2(payloadResp, payload)
	if err != nil {
		if useCache {
			if stale, ok := aclDecisionCacheV2.getStale(cacheKey, now); ok {
				return stale, nil
			}
		}
		return nil, &controlPlaneError{Status: http.StatusServiceUnavailable, Err: err}
	}
	if useCache {
		aclDecisionCacheV2.set(cacheKey, decision, controlPlaneFreshTTL(payloadResp.CacheTTLSeconds), controlPlaneStaleIfError(), now)
	}
	return &decision, nil
}

func authorizeRequestForPeers(ctx context.Context, authHeader string, peerIDs []string) (*controlPlaneDecision, *controlPlaneError) {
	if !isControlPlaneEnabled() {
		return nil, nil
	}
	bearer, err := parseBearerToken(authHeader)
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusUnauthorized, Err: err}
	}
	return evaluateBearerForPeers(ctx, bearer, peerIDs)
}

func authorizeRequestForScope(ctx context.Context, authHeader string, scope routingScope, peerIDs []string, upstreamPeerID string) (*controlPlaneDecisionV2, *controlPlaneError) {
	if !isControlPlaneEnabled() {
		return nil, nil
	}
	bearer, err := parseBearerToken(authHeader)
	if err != nil {
		return nil, &controlPlaneError{Status: http.StatusUnauthorized, Err: err}
	}
	return evaluateBearerForScope(ctx, bearer, scope, peerIDs, upstreamPeerID)
}

// authorizeKeyHashForScope is like authorizeRequestForScope but accepts a
// pre-computed key hash — typically forwarded by a trusted head via
// X-Otela-Key-Hash — so the raw client API key never has to reach the
// worker. On trusted routes the worker's own node credential is used for
// the control-plane call (set inside evaluateKeyHashForScope), not the
// client's key.
func authorizeKeyHashForScope(ctx context.Context, keyHash string, scope routingScope, peerIDs []string, upstreamPeerID string) (*controlPlaneDecisionV2, *controlPlaneError) {
	if !isControlPlaneEnabled() {
		return nil, nil
	}
	keyHash = strings.TrimSpace(keyHash)
	if keyHash == "" {
		return nil, &controlPlaneError{Status: http.StatusUnauthorized, Err: errMissingBearer}
	}
	return evaluateKeyHashForScope(ctx, keyHash, scope, peerIDs, upstreamPeerID)
}

// verifyBearerToken calls the legacy auth server to resolve a bearer token into
// a wallet public key. Returns ("", nil) if the legacy auth server is not
// configured or the control-plane integration is enabled.
func verifyBearerToken(ctx context.Context, token string) (string, error) {
	if isControlPlaneEnabled() {
		return "", nil
	}
	authURL := viper.GetString("security.auth_url")
	if authURL == "" {
		return "", nil
	}

	// Check cache first.
	if wallet, ok := tokenCache.get(token); ok {
		return wallet, nil
	}

	url := strings.TrimRight(authURL, "/") + "/api/keys/verify"
	body := fmt.Sprintf(`{"token":%q}`, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := legacyAuthHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("invalid or revoked token")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth server returned %d", resp.StatusCode)
	}

	var result authVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid auth response: %w", err)
	}

	tokenCache.set(token, result.Wallet)
	return result.Wallet, nil
}

// resolveClientWallet extracts the client's wallet from the Authorization
// header by verifying the bearer token against the auth server.
// If auth is not configured or no token is present, returns "".
func resolveClientWallet(c *gin.Context) string {
	token, err := parseBearerToken(c.GetHeader("Authorization"))
	if err != nil {
		return ""
	}

	wallet, err := verifyBearerToken(c.Request.Context(), token)
	if err != nil {
		common.Logger.Warnf("Legacy bearer token verification failed: %v", err)
		return ""
	}
	return wallet
}
