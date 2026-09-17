package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opentela/internal/attestation"
	"opentela/internal/common"
	"opentela/internal/platform"
	"opentela/internal/protocol/nodetable"
	"opentela/internal/protocol/swim"
	"opentela/internal/wallet"
	"os"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
)

var dntOnce sync.Once
var myself Peer
var myselfMu sync.RWMutex

var (
	scalableNodeTable *nodetable.NodeTable
	nodeTableWriter   *nodetable.Writer
	swimOnce          sync.Once
	swimInstance      *swim.SWIM
)

// InitScalableNodeTable sets up the new COW node table and SWIM.
// Called from server.go when scalability.swim_enabled=true.
func InitScalableNodeTable() {
	swimOnce.Do(func() {
		scalableNodeTable = nodetable.NewNodeTable()
		nodeTableWriter = nodetable.NewWriter(scalableNodeTable)
		nodeTableWriter.Start()
	})
}

func GetScalableSnapshot() *nodetable.NodeTableSnapshot {
	if scalableNodeTable == nil {
		return nil
	}
	return scalableNodeTable.Snapshot()
}

func GetNodeTableWriter() *nodetable.Writer {
	return nodeTableWriter
}

// StartSWIM initializes and runs the SWIM membership protocol.
// Must be called after InitScalableNodeTable() and GetP2PNode().
func StartSWIM(ctx context.Context) {
	host, _ := GetP2PNode(nil)
	eventCh := make(chan swim.MemberEvent, 1024)

	cfg := swim.Config{
		ProbeInterval:        viper.GetDuration("swim.probe_interval"),
		ProbeTimeout:         viper.GetDuration("swim.probe_timeout"),
		IndirectProbeTimeout: viper.GetDuration("swim.indirect_probe_timeout"),
		IndirectProbes:       viper.GetInt("swim.indirect_probes"),
		SuspectTimeout:       viper.GetDuration("swim.suspect_timeout"),
		RetransmitMult:       viper.GetInt("swim.retransmit_mult"),
	}

	transport := swim.NewLibP2PTransport(host, cfg.ProbeTimeout)
	swimInstance = swim.NewSWIM(host.ID(), cfg, transport, eventCh)
	swim.RegisterHandler(host, swimInstance)

	// Seed SWIM from existing libp2p connections
	for _, conn := range host.Network().Conns() {
		swimInstance.AddMember(conn.RemotePeer())
	}

	// Forward SWIM events to node table writer
	go func() {
		for ev := range eventCh {
			var eventType nodetable.EventType
			switch ev.Status {
			case swim.StatusJoin:
				eventType = nodetable.EventSWIMJoin
			case swim.StatusAlive:
				eventType = nodetable.EventSWIMAlive
			case swim.StatusSuspect:
				eventType = nodetable.EventSWIMSuspect
			case swim.StatusDead:
				eventType = nodetable.EventSWIMDead
			}

			ne := nodetable.NodeEvent{
				Type:      eventType,
				PeerID:    ev.Peer,
				Timestamp: time.Now().Unix(),
			}

			// Parse metadata if present
			if len(ev.Meta) > 0 {
				var meta swim.Metadata
				if err := meta.Unmarshal(ev.Meta); err == nil {
					pd := &nodetable.PeerData{
						IdentityGroups: meta.IdentityGroups,
						ActiveRequests: meta.ActiveRequests,
						RegionHint:     meta.RegionHint,
					}
					// Convert RoleType to []string
					switch meta.Role {
					case swim.RoleWorker:
						pd.Role = []string{"worker"}
					case swim.RoleHead:
						pd.Role = []string{"head"}
					case swim.RoleRelay:
						pd.Role = []string{"relay"}
					}
					ne.PeerData = pd
				}
			}

			nodeTableWriter.Send(ne)
		}
	}()

	// Run SWIM protocol
	go swimInstance.Run(ctx)
	common.Logger.Info("SWIM membership protocol started")
}

const (
	CONNECTED    string = "connected"
	DISCONNECTED string = "disconnected"
	LEFT         string = "left"
	PENDING      string = "pending"
	READY        string = "ready"
)

type Service struct {
	Name     string              `json:"name"`
	Hardware common.HardwareSpec `json:"hardware"`
	Status   string              `json:"status"`
	Host     string              `json:"host"`
	Port     string              `json:"port"`
	// IdentityGroup is a list of identities that can access this service
	// Format: <identity_group_name>=<identity_name>
	// e.g., "model=resnet50"
	IdentityGroup []string `json:"identity_group"`
}

const RuntimeCapabilityServicePolicyV2 = "service-policy-v2"

// Trust levels for nodes in the network.
const (
	TrustUntrusted    = 0 // no attestation or invalid
	TrustSelfAttested = 1 // wallet signature verified
	TrustUserTrusted  = 2 // explicitly trusted by the requesting user
	TrustKYCVerified  = 3 // passed KYC (future)
)

// Peer is a single node in the network, as can be seen by the current node.
type Peer struct {
	ID         string `json:"id"`
	Latency    int    `json:"latency"` // in ms
	Privileged bool   `json:"privileged"`
	// Owner is the wallet public key (base58) of the node operator.
	// This field is always a raw wallet pubkey and is used for
	// trust/access-control decisions.
	Owner string `json:"owner"`
	// ProviderID is the deterministic human-readable identifier derived
	// from the wallet pubkey (e.g. "otela-AbCdEfGh...").  It is stored
	// separately so that Owner always carries the raw pubkey and callers
	// never confuse the two.
	ProviderID        string              `json:"provider_id,omitempty"`
	CurrentOffering   []string            `json:"current_offering"`
	Role              []string            `json:"role"`
	Status            string              `json:"status"`
	AvailableOffering []string            `json:"available_offering"`
	Service           []Service           `json:"service"`
	LastSeen          int64               `json:"last_seen"`
	Version           string              `json:"version"`
	Hostname          string              `json:"hostname"`
	Labels            map[string]string   `json:"labels,omitempty"`
	PublicAddress     string              `json:"public_address"`
	PublicPort        string              `json:"public_port,omitempty"`
	Hardware          common.HardwareSpec `json:"hardware"`
	// RelayPeer is the peer ID of the relay this node has an active
	// reservation on. Set by workers behind firewalls so head nodes
	// know which relay to route through.
	RelayPeer string `json:"relay_peer,omitempty"`
	// EvictedBy names the head that retired this peer after its relay reported
	// no reservation for it. Empty for a self-announced departure.
	//
	// This is provenance, not authentication: it is self-reported and therefore
	// spoofable. It exists so a buggy or misconfigured non-head cannot retire
	// healthy workers, and so operators can tell an eviction from a graceful
	// leave. Note that go-ds-crdt already lets any participant write any key, so
	// this does not create a new capability — signing eviction records with the
	// evictor's wallet identity is the real fix, and belongs with the broader
	// CRDT write-authorization model.
	EvictedBy string `json:"evicted_by,omitempty"`
	Connected bool   `json:"connected"`
	Load      []int  `json:"load"`
	// BuildAttestation carries the version + commit + signature so peers
	// can verify that this node is running an officially signed binary.
	// Absent (nil) for nodes running older versions without attestation.
	BuildAttestation *attestation.BuildInfo `json:"build_attestation,omitempty"`
	// SignedBuild is set locally (not serialised from remote) after
	// verifying BuildAttestation.  true = signature valid.
	SignedBuild bool `json:"-"`
	// IdentityAttestation proves this node's operator controls the
	// wallet key listed in Owner.  Nil for old nodes without attestation.
	IdentityAttestation *wallet.IdentityAttestation `json:"identity_attestation,omitempty"`
	// TrustLevel is computed locally after verifying attestations.
	// 0=untrusted, 1=self-attested, 2=user-trusted, 3=KYC-verified.
	TrustLevel int `json:"-"`
	// RuntimeCapabilities advertises worker/runtime authorization features that
	// are safe for the control plane to gate on.
	RuntimeCapabilities []string `json:"capabilities,omitempty"`
	// ServiceNameCounts reports the observed multiplicity of exact local service
	// names so the control plane can fail mixed-service activation closed when a
	// name is ambiguous on the worker.
	ServiceNameCounts map[string]int `json:"service_name_counts,omitempty"`
}

// parseLabels turns repeated --label key=value entries from viper into a map.
// Malformed entries (no '=' or empty key) are dropped with a debug log.
func parseLabels() map[string]string {
	raw := viper.GetStringSlice("label")
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for _, entry := range raw {
		k, v, ok := strings.Cut(entry, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			common.Logger.Debugf("ignoring malformed label %q (expected key=value)", entry)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type PeerWithStatus struct {
	ID            string `json:"id"`
	Connectedness string `json:"connectedness"` // "connected" or "disconnected"
}

// Node table tracks the nodes and their status in the network.
type NodeTable map[string]Peer

var dnt *NodeTable
var tableUpdateSem = make(chan struct{}, 1) // capacity 1 → max 1 goroutine at a time

func getNodeTable() *NodeTable {
	dntOnce.Do(func() {
		dnt = &NodeTable{}
	})
	return dnt
}

func UpdateNodeTable(peer Peer) {
	ctx := context.Background()
	host, _ := GetP2PNode(nil)
	// broadcast the peer to the network
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	peer.ID = host.ID().String()
	// merge services instead of overwriting
	// first find the peer in the table if it exists
	existingPeer, err := GetPeerFromTable(peer.ID)
	if err == nil {
		peer.Service = append(peer.Service, existingPeer.Service...)
		// Preserve existing wallet pubkey and provider ID if not set in the update.
		if peer.Owner == "" && existingPeer.Owner != "" {
			peer.Owner = existingPeer.Owner
		}
		if peer.ProviderID == "" && existingPeer.ProviderID != "" {
			peer.ProviderID = existingPeer.ProviderID
		}
	}
	// Track services in localServices so ReannounceLocalServices preserves them.
	for _, svc := range peer.Service {
		addLocalService(svc)
	}
	if viper.GetString("public-addr") != "" {
		peer.PublicAddress = viper.GetString("public-addr")
	}
	peer.PublicPort = viper.GetString("tcpport")
	applyLocalRuntimeMetadata(&peer)
	value, err := json.Marshal(peer)
	common.ReportError(err, "Error while marshalling peer")
	if err := store.Put(ctx, key, value); err != nil {
		common.Logger.Error("Error while updating node table: ", err)
	}
}

func MarkSelfAsBootstrap() {
	if viper.GetString("public-addr") != "" || viper.GetString("role") == "relay" {
		common.Logger.Debug("Registering as bootstrap node")
		ctx := context.Background()
		store, _ := GetCRDTStore()
		host, _ := GetP2PNode(nil)
		key := ds.NewKey(host.ID().String())
		// Ensure the global `myself` has at least a stable ID before marshalling.
		myselfMu.Lock()
		if myself.ID == "" {
			myself.ID = host.ID().String()
		}
		// Use the global myself which carries build attestation.
		myself.PublicAddress = viper.GetString("public-addr")
		myself.PublicPort = viper.GetString("tcpport")
		myself.Connected = true
		applyLocalRuntimeMetadata(&myself)
		value, err := json.Marshal(myself)
		myselfMu.Unlock()
		UpdateNodeTableHook(key, value)
		common.ReportError(err, "Error while marshalling peer")
		if err := store.Put(ctx, key, value); err != nil {
			common.Logger.Error("Error while registering bootstrap: ", err)
		}
	}
}

func AnnounceLeave() {
	ctx := context.Background()
	host, _ := GetP2PNode(nil)
	// broadcast the peer to the network
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	common.Logger.Info("Leaving network")

	// Update self status to LEFT
	myselfMu.Lock()
	myself.Status = LEFT
	myself.Connected = false
	myself.LastSeen = time.Now().Unix()
	applyLocalRuntimeMetadata(&myself)
	value, err := json.Marshal(myself)
	myselfMu.Unlock()
	if err != nil {
		common.Logger.Warn("Error while marshalling peer for leave: ", err)
		return
	}

	if err := store.Put(ctx, key, value); err != nil {
		// Failure here means peers only learn of our departure via
		// liveness eviction (ghost until then).
		common.Logger.Warn("Error while announcing leave: ", err)
	}
}

func UpdateNodeTableHook(key ds.Key, value []byte) {
	table := *getNodeTable()
	var peer Peer
	err := json.Unmarshal(value, &peer)
	if err != nil {
		// Hot path: runs for every CRDT update, and the value comes from
		// remote peers — one peer publishing bad data must not be able to
		// spam our error log.
		common.Logger.Debug("Error while unmarshalling peer: ", err)
	}

	// Verify build attestation from the remote peer.
	peer.SignedBuild = false
	if peer.BuildAttestation != nil {
		if err := attestation.Verify(*peer.BuildAttestation); err == nil {
			peer.SignedBuild = true
		} else {
			common.Logger.Debugf("Peer [%s] build attestation invalid: %v", peer.ID, err)
		}
	}

	// If enforcement is on, reject peers without a valid signed build.
	// Always trust ourselves — a node should never reject its own entry.
	if viper.GetBool("security.require_signed_binary") && !peer.SignedBuild && peer.ID != MyID {
		common.Logger.Debugf("Rejecting peer [%s]: no valid build attestation (security.require_signed_binary=true)", peer.ID)
		return
	}

	// Compute trust level from identity attestation.
	peer.TrustLevel = TrustUntrusted
	if peer.IdentityAttestation != nil {
		if err := wallet.VerifyIdentity(peer.IdentityAttestation); err == nil {
			peer.TrustLevel = TrustSelfAttested
			// Check if this peer's wallet is in our trusted wallets list.
			for _, tw := range viper.GetStringSlice("trusted_wallets") {
				if tw == peer.IdentityAttestation.WalletPubkey {
					peer.TrustLevel = TrustUserTrusted
					break
				}
			}
			// Self-trust: if our own wallet matches the peer's wallet.
			if peer.IdentityAttestation.WalletPubkey == viper.GetString("wallet.account") {
				if peer.TrustLevel < TrustUserTrusted {
					peer.TrustLevel = TrustUserTrusted
				}
			}
		} else {
			common.Logger.Warnf("Peer [%s] identity attestation invalid: %v", peer.ID, err)
		}
	}

	// Check for Left status — keep the peer in the table marked as LEFT
	// so TombstoneManager.collectCandidates can find it for deferred cleanup.
	if peer.Status == LEFT {
		// An eviction record is one node asserting that a *different* node is
		// gone, so it needs more scrutiny than a self-announced leave. Act on it
		// when we can confirm the evictor is a head we already know about, or
		// when the evictor is the peer's own advertised relay — the relay is the
		// authority on whether the worker is still attached, and the record
		// itself carries that match (EvictedBy == RelayPeer), so no table
		// lookup is needed. That second path is what lets a dispatcher that
		// runs the relay in-process publish verdicts mesh-wide even when its
		// configured role is not "head".
		// Checked before taking tableUpdateSem: isKnownHeadPeer reads the table
		// itself and would otherwise deadlock.
		if peer.EvictedBy != "" && peer.EvictedBy != peer.RelayPeer && !isKnownHeadPeer(peer.EvictedBy) {
			common.Logger.Warnf("Ignoring eviction of [%s] by [%s]: evictor is neither a known head nor the peer's relay",
				peer.ID, peer.EvictedBy)
			return
		}
		common.Logger.Debugf("Peer [%s] has left, marking as LEFT in table", peer.ID)
		tableUpdateSem <- struct{}{}
		defer func() { <-tableUpdateSem }() // Release on exit
		peer.Connected = false
		if peer.LastSeen == 0 {
			peer.LastSeen = time.Now().Unix()
		}
		table[key.String()] = peer
		return
	}

	// A non-LEFT update: if this peer was previously LEFT, it has rejoined.
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	if existing, ok := table[key.String()]; ok {
		if existing.Status == LEFT {
			common.Logger.Debugf("Peer [%s] rejoined (was LEFT)", peer.ID)
		}
		// LastSeen is monotonic: keep whichever timestamp is newer. This covers
		// both an update that omits it and — more importantly — a CRDT
		// rebroadcast carrying a peer's own stale value, which would otherwise
		// drag the timestamp backwards past liveness we have since proven
		// locally. A service-less peer dragged back far enough trips the
		// 2-minute disconnect window and flaps.
		if existing.LastSeen > peer.LastSeen {
			peer.LastSeen = existing.LastSeen
		}
	}
	// LastSeen means "last proven alive", and it is owned by the caller: only a
	// successful liveness check or a peer's own announcement may advance it.
	//
	// This used to restamp unconditionally, which made every update look like
	// fresh evidence of life — including updates that recorded a FAILED ping
	// (clock.go) and disconnect notifications that explicitly asked to keep the
	// last known good value (host.go DisconnectedF). The staleness sweep reasons
	// on this field, so it could never fire while a peer was still being
	// processed, and froze the moment the peer left the peerstore. Dead workers
	// therefore stayed connected:true indefinitely.
	//
	// Only stamp a first sighting, where there is no prior value to preserve.
	if peer.LastSeen == 0 {
		peer.LastSeen = time.Now().Unix()
	}
	table[key.String()] = peer
}

func DeleteNodeTableHook(key ds.Key) {
	table := *getNodeTable()
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	delete(table, key.String())
}

// DeletePeerRow removes a peer's row from the node table and publishes the
// deletion through the CRDT so the whole mesh converges on it. It is the
// operator escape hatch for rows that are provably dead but were never
// evicted — dead workers whose head could not reach a verdict (see
// ProbePeerLiveness), or rows left behind by a bug — exposed via
// DELETE /v1/dnt/_node.
//
// Local-first: the in-memory row is dropped even when the CRDT publish
// fails, and routing only consults the in-memory table, so a ghost stops
// being served immediately. A CRDT delete cannot evict a peer that is
// actually alive: its next registration rewrites the key and wins.
func DeletePeerRow(peerID string) error {
	if strings.TrimSpace(peerID) == "" {
		return errors.New("peer id is required")
	}
	if peerID == MyID {
		return errors.New("refusing to delete own row; stop the process or announce a leave instead")
	}
	if _, err := GetPeerFromTable(peerID); err != nil {
		return fmt.Errorf("peer not found: %w", err)
	}
	DeleteNodeTableHook(ds.NewKey(peerID))
	// Read the store variable directly rather than GetCRDTStore(), which
	// force-initializes the whole CRDT/IPFS stack — wrong for an operator
	// call that must work before or without CRDT connectivity. Same pattern
	// as TriggerResync.
	if crdtStore == nil {
		// No CRDT connectivity: local-only delete. Each node's own sweep
		// reaches the same conclusion independently, so absence of the publish
		// only delays convergence, it does not prevent it.
		return nil
	}
	if err := crdtStore.Delete(context.Background(), ds.NewKey(peerID)); err != nil {
		return fmt.Errorf("row deleted locally, but CRDT publish failed: %w", err)
	}
	return nil
}

func GetPeerFromTable(peerId string) (Peer, error) {
	table := *getNodeTable()
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	peer, ok := table["/"+peerId]
	if !ok {
		return Peer{}, errors.New("peer not found")
	}
	return peer, nil
}

func GetConnectedPeers() *NodeTable {
	var connected = NodeTable{}
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	for id, p := range *getNodeTable() {
		if p.Connected {
			connected[id] = p
		}
	}
	return &connected
}

func GetAllPeers() *NodeTable {
	var peers = NodeTable{}
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	for id, p := range *getNodeTable() {
		peers[id] = p
	}
	return &peers
}

func GetService(name string) (Service, error) {
	host, _ := GetP2PNode(nil)
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	value, err := store.Get(context.Background(), key)
	if err != nil {
		common.Logger.Debug("Error while getting peer: ", err)
	}
	var peer Peer
	err = json.Unmarshal(value, &peer)
	if err != nil {
		common.Logger.Debug("Error while unmarshalling peer: ", err)
	}
	for _, service := range peer.Service {
		if service.Name == name {
			return service, nil
		}
	}
	return Service{}, errors.New("Service not found")
}

func LocalServiceNameCounts() map[string]int {
	return serviceNameCounts(snapshotLocalServices())
}

func LocalServicesByName(name string) []Service {
	services := snapshotLocalServices()
	matches := make([]Service, 0, len(services))
	for _, service := range services {
		if service.Name == name {
			matches = append(matches, service)
		}
	}
	return matches
}

func GetAllProviders(serviceName string) ([]Peer, error) {
	var providers []Peer
	table := *getNodeTable()
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	for _, peer := range table {
		if !peer.Connected {
			continue
		}
		for _, service := range peer.Service {
			if service.Name == serviceName {
				providers = append(providers, peer)
				break
			}
		}
	}
	if len(providers) == 0 {
		return providers, errors.New("no providers found")
	}
	return providers, nil
}

// InitializeMyself registers this node in the CRDT.
// walletPubkeyOverride is the raw wallet public key (base58) for this node
// (may be empty).  It is stored in Owner so that all trust/access-control
// comparisons work with like-for-like values.
// wm is the wallet manager for signing identity attestations and deriving the
// ProviderID (may be nil if no wallet is configured).
func InitializeMyself(walletPubkeyOverride string, wm *wallet.WalletManager) {
	host, _ := GetP2PNode(nil)
	ctx := context.Background()
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	hn, _ := os.Hostname()
	myselfMu.Lock()
	myself = Peer{
		ID:            host.ID().String(),
		PublicAddress: viper.GetString("public-addr"),
		LastSeen:      time.Now().Unix(),
		Connected:     true,
		Status:        PENDING,
		Version:       common.JSONVersion.Version,
		Hostname:      hn,
		Labels:        parseLabels(),
	}

	// Attach build attestation so peers can verify we run a signed binary.
	if common.JSONVersion.Version != "" && common.JSONVersion.Commit != "" {
		myself.BuildAttestation = &attestation.BuildInfo{
			Version:   common.JSONVersion.Version,
			Commit:    common.JSONVersion.Commit,
			Signature: common.JSONVersion.BuildSig,
		}
		if err := attestation.Verify(*myself.BuildAttestation); err == nil {
			myself.SignedBuild = true
			common.Logger.Info("Build attestation verified")
		} else {
			common.Logger.Debugf("Build attestation not verified: %v", err)
		}
	}

	// Owner always holds the raw wallet public key so access-control
	// comparisons are like-for-like (wallet pubkey vs wallet pubkey).
	if walletPubkeyOverride != "" {
		myself.Owner = walletPubkeyOverride
		common.Logger.Debugf("Wallet provider: %s (verified)", myself.Owner)
	} else if account := viper.GetString("wallet.account"); account != "" {
		myself.Owner = account
		common.Logger.Debugf("Wallet provider: %s (configured)", myself.Owner)
	} else if wm != nil && wm.WalletExists() {
		myself.Owner = wm.GetPublicKey()
		if myself.Owner != "" {
			common.Logger.Debugf("Wallet provider: %s (local)", myself.Owner)
		}
	}

	// Store the human-readable provider ID separately so it never
	// overwrites the wallet pubkey in Owner.
	if wm != nil && wm.WalletExists() {
		if pid := wm.GetProviderID(); pid != "" {
			myself.ProviderID = pid
			common.Logger.Debugf("Provider ID: %s", myself.ProviderID)
		}
	}

	// Sign identity attestation binding our peer ID to our wallet key.
	if wm != nil && wm.WalletExists() {
		att, err := wallet.SignIdentity(myself.ID, time.Now().Unix(), wm)
		if err != nil {
			common.Logger.Warnf("Could not sign identity attestation: %v", err)
		} else {
			myself.IdentityAttestation = att
			myself.TrustLevel = TrustSelfAttested
			common.Logger.Info("Identity attestation signed (self-attested)")
		}
	}

	myself.Hardware.GPUs = platform.GetGPUInfo()

	// Set role from config.
	if role := viper.GetString("role"); role != "" {
		myself.Role = []string{role}
	}

	// All nodes carry their own port so ConnectedBootstraps builds correct multiaddrs.
	myself.PublicPort = viper.GetString("tcpport")

	// Warn if relay has no public address — it won't be discoverable as a bootstrap.
	if viper.GetString("role") == "relay" && myself.PublicAddress == "" {
		common.Logger.Warn("Relay node has no public-addr set; it won't be discoverable as a bootstrap")
	}

	applyLocalRuntimeMetadata(&myself)

	value, err := json.Marshal(myself)
	myselfMu.Unlock()
	common.ReportError(err, "Error while marshalling peer")
	err = store.Put(ctx, key, value)
	if err != nil {
		common.Logger.Error("Error while initializing myself in the node table: ", err)
	}
}

// GetSelf returns a copy of this node's own Peer record.
func GetSelf() Peer {
	myselfMu.RLock()
	defer myselfMu.RUnlock()
	return myself
}

// SetMyselfRelayPeer atomically updates the RelayPeer field on myself.
func SetMyselfRelayPeer(relayPeer string) {
	myselfMu.Lock()
	defer myselfMu.Unlock()
	myself.RelayPeer = relayPeer
	applyLocalRuntimeMetadata(&myself)
}

// SetMyselfForTest sets the myself var for testing. Test-only.
func SetMyselfForTest(p Peer) {
	myselfMu.Lock()
	defer myselfMu.Unlock()
	myself = p
}

// RegisterRemotePeer writes a remote peer's entry to the CRDT store.
// Used by the HTTP registration endpoint to insert relay addresses.
func RegisterRemotePeer(p Peer) error {
	ctx := context.Background()
	store, _ := GetCRDTStore()
	key := ds.NewKey(p.ID)
	p.Connected = false
	p.LastSeen = time.Now().Unix()
	value, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal remote peer: %w", err)
	}
	UpdateNodeTableHook(key, value)
	return store.Put(ctx, key, value)
}

func runtimeCapabilities() []string {
	return []string{RuntimeCapabilityServicePolicyV2}
}

func serviceNameCounts(services []Service) map[string]int {
	if len(services) == 0 {
		return nil
	}
	counts := make(map[string]int, len(services))
	for _, service := range services {
		counts[service.Name]++
	}
	return counts
}

func applyLocalRuntimeMetadata(peer *Peer) {
	if peer == nil {
		return
	}
	peer.RuntimeCapabilities = runtimeCapabilities()
	peer.ServiceNameCounts = serviceNameCounts(peer.Service)
}

// isKnownHeadPeer reports whether id names a peer we already know carries the
// head role. Used to filter eviction records: see the EvictedBy field for why
// this is a sanity check rather than an authentication boundary.
func isKnownHeadPeer(id string) bool {
	p, err := GetPeerFromTable(id)
	if err != nil {
		return false
	}
	for _, r := range p.Role {
		if r == "head" {
			return true
		}
	}
	return false
}
