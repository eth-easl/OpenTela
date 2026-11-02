package protocol

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opentela/internal/common"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
)

// A worker (no public-addr) is only routable from the head if it holds a live
// connection to a bootstrap peer AND a live relay reservation on the relay it
// advertises in the CRDT (RelayPeer). Both are established once at startup
// (the reservation 10 s after the CRDT store starts); if either attempt races
// the network coming up, the node stays healthy locally yet unreachable.
// startAutoReconnect only acts when there are zero connections and Reconnect()
// fires with 50% probability per minute, so recovery was slow and unreliable.
// connectivityWatchdog closes that gap by retrying until both conditions hold.

const (
	bootstrapRefreshInterval = 10 * time.Minute
	reservationSafetyMargin  = time.Minute
)

// ---------------------------------------------------------------------------
// Bootstrap peer cache
// ---------------------------------------------------------------------------

// bootstrapCache holds the resolved bootstrap peers so that health checks are
// pure in-memory reads. Resolution may hit HTTP/DNS discovery sources, so it
// only happens on refresh (startup, reconnect attempts, or when stale). A
// failed or empty resolution keeps the last known set, so a discovery outage
// does not make a connected node look disconnected.
type bootstrapCache struct {
	mu    sync.RWMutex
	peers map[peer.ID]peer.AddrInfo
	at    time.Time
}

var bootstraps = &bootstrapCache{}

// parseBootstrapAddrInfos converts multiaddrs to AddrInfos one by one, skipping
// entries without a valid /p2p/<id> component (and self) instead of failing
// the whole batch.
func parseBootstrapAddrInfos(addrs []multiaddr.Multiaddr, self peer.ID) map[peer.ID]peer.AddrInfo {
	out := map[peer.ID]peer.AddrInfo{}
	for _, a := range addrs {
		ai, err := peer.AddrInfoFromP2pAddr(a)
		if err != nil {
			common.Logger.Debugf("Skipping bootstrap address %s: %v", a, err)
			continue
		}
		if ai.ID == self {
			continue
		}
		cur := out[ai.ID]
		cur.ID = ai.ID
		cur.Addrs = append(cur.Addrs, ai.Addrs...)
		out[ai.ID] = cur
	}
	return out
}

// refresh re-resolves the bootstrap list. It keeps the previous set when the
// new resolution yields nothing.
func (b *bootstrapCache) refresh(self peer.ID) {
	next := parseBootstrapAddrInfos(getDefaultBootstrapPeers(nil, viper.GetString("mode")), self)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(next) == 0 {
		if len(b.peers) > 0 {
			common.Logger.Debug("Bootstrap refresh returned no peers; keeping last known set")
		}
		return
	}
	b.peers, b.at = next, time.Now()
}

// refreshIfStale refreshes when the cache is empty or older than the refresh
// interval.
func (b *bootstrapCache) refreshIfStale(self peer.ID) {
	b.mu.RLock()
	stale := len(b.peers) == 0 || time.Since(b.at) > bootstrapRefreshInterval
	b.mu.RUnlock()
	if stale {
		b.refresh(self)
	}
}

func (b *bootstrapCache) list() []peer.AddrInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]peer.AddrInfo, 0, len(b.peers))
	for _, ai := range b.peers {
		out = append(out, ai)
	}
	return out
}

func (b *bootstrapCache) contains(p peer.ID) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.peers[p]
	return ok
}

func (b *bootstrapCache) empty() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.peers) == 0
}

// connected reports whether h holds a live connection to any cached bootstrap
// peer. No network or discovery I/O.
func (b *bootstrapCache) connected(h host.Host) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for id := range b.peers {
		if h.Network().Connectedness(id) == network.Connected {
			return true
		}
	}
	return false
}

// IsBootstrapConnected reports whether this node has a live connection to at
// least one known bootstrap peer other than itself.
func IsBootstrapConnected() bool {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return false
	}
	return bootstraps.connected(h)
}

// ---------------------------------------------------------------------------
// Relay reservation state
// ---------------------------------------------------------------------------

// relayReservations tracks, per relay peer, the expiry of the latest manual
// reservation (MakeRelayReservations) and whether AutoRelay currently
// advertises a circuit address through it.
var relayReservations = struct {
	mu        sync.Mutex
	expires   map[peer.ID]time.Time
	autoRelay map[peer.ID]bool
}{expires: map[peer.ID]time.Time{}, autoRelay: map[peer.ID]bool{}}

// recordRelayReservation stores the expiry of a successful reservation on p.
func recordRelayReservation(p peer.ID, expires time.Time) {
	relayReservations.mu.Lock()
	defer relayReservations.mu.Unlock()
	if expires.After(relayReservations.expires[p]) {
		relayReservations.expires[p] = expires
	}
}

// forgetRelayReservation drops the manual reservation state for p. Called when
// the last connection to p closes: a relay v2 reservation does not survive the
// connection it was made on.
func forgetRelayReservation(p peer.ID) {
	relayReservations.mu.Lock()
	defer relayReservations.mu.Unlock()
	delete(relayReservations.expires, p)
}

// setAutoRelayAddrs replaces the set of relays that AutoRelay holds circuit
// addresses through.
func setAutoRelayAddrs(addrs []multiaddr.Multiaddr) {
	next := map[peer.ID]bool{}
	for _, a := range addrs {
		if p, ok := circuitRelayPeer(a); ok {
			next[p] = true
		}
	}
	relayReservations.mu.Lock()
	relayReservations.autoRelay = next
	relayReservations.mu.Unlock()
}

// circuitRelayPeer returns the relay peer ID of a /.../p2p/<relay>/p2p-circuit
// address.
func circuitRelayPeer(a multiaddr.Multiaddr) (peer.ID, bool) {
	relay, _, found := strings.Cut(a.String(), "/p2p-circuit")
	if !found {
		return "", false
	}
	ra, err := multiaddr.NewMultiaddr(relay)
	if err != nil {
		return "", false
	}
	ai, err := peer.AddrInfoFromP2pAddr(ra)
	if err != nil {
		return "", false
	}
	return ai.ID, true
}

// relayValid reports whether this node holds a usable reservation on p: p is
// connected, and either a manual reservation on p is unexpired (with a safety
// margin) or AutoRelay advertises a circuit address through p.
func relayValid(h host.Host, p peer.ID) bool {
	if h.Network().Connectedness(p) != network.Connected {
		return false
	}
	relayReservations.mu.Lock()
	defer relayReservations.mu.Unlock()
	if time.Until(relayReservations.expires[p]) > reservationSafetyMargin {
		return true
	}
	return relayReservations.autoRelay[p]
}

// advertisedRelayValid reports whether the RelayPeer this node advertises in
// the CRDT (the one heads route through) holds a usable reservation.
func advertisedRelayValid(h host.Host) bool {
	p, err := peer.Decode(GetSelf().RelayPeer)
	if err != nil {
		return false
	}
	return relayValid(h, p)
}

// HasRelayReservation reports whether the relay this node advertises to the
// network holds a live reservation for it.
func HasRelayReservation() bool {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return false
	}
	return advertisedRelayValid(h)
}

// watchAutoRelayAddrs mirrors AutoRelay's circuit addresses into
// relayReservations until ctx ends.
func watchAutoRelayAddrs(ctx context.Context, h host.Host) {
	sub, err := h.EventBus().Subscribe(new(event.EvtAutoRelayAddrsUpdated))
	if err != nil {
		common.Logger.Debugf("Cannot subscribe to AutoRelay address updates: %v", err)
		return
	}
	defer func() { _ = sub.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Out():
			if !ok {
				return
			}
			if evt, ok := e.(event.EvtAutoRelayAddrsUpdated); ok {
				setAutoRelayAddrs(evt.RelayAddrs)
			}
		}
	}
}

// relayReservationsRunning guards MakeRelayReservations against overlapping
// runs from the startup path, Reconnect() and the watchdog.
var relayReservationsRunning atomic.Bool

// ---------------------------------------------------------------------------
// Watchdog
// ---------------------------------------------------------------------------

// connectivityWatchdogEnabled mirrors the condition under which AutoRelay is
// enabled in newHost, and excludes standalone mode (no bootstrap peers, so the
// watchdog could never succeed).
func connectivityWatchdogEnabled() bool {
	reachability := strings.ToLower(strings.TrimSpace(viper.GetString("reachability")))
	return reachability != "public" &&
		viper.GetString("public-addr") == "" &&
		viper.GetString("mode") != "standalone"
}

// connectivityWatchdog drives a node towards "bootstrap connected and
// advertised relay reserved". All side effects are injected so step() is
// unit-testable.
type connectivityWatchdog struct {
	haveBootstraps     func() bool
	bootstrapConnected func() bool
	relayReserved      func() bool
	reconnect          func(ctx context.Context) // refresh + dial bootstraps
	reserve            func()                    // MakeRelayReservations

	attempt     int
	reported    bool
	lastOK      bool
	noBootstrap bool
}

// step performs one check and returns the delay before the next one.
func (w *connectivityWatchdog) step(ctx context.Context) time.Duration {
	const (
		healthyInterval = 30 * time.Second
		minBackoff      = 5 * time.Second
		maxBackoff      = time.Minute
	)
	if !w.haveBootstraps() {
		if !w.noBootstrap {
			common.Logger.Warn("Connectivity watchdog: no bootstrap peers resolved; will retry discovery")
			w.noBootstrap = true
		}
		w.reconnect(ctx) // refreshes discovery
		return maxBackoff
	}
	w.noBootstrap = false

	bs, rr := w.bootstrapConnected(), w.relayReserved()
	ok := bs && rr
	if !w.reported || w.lastOK != ok {
		if ok {
			common.Logger.Info("Connectivity OK: bootstrap connected and advertised relay reserved")
		} else {
			common.Logger.Warnf("Connectivity incomplete: bootstrap_connected=%v relay_reserved=%v; retrying", bs, rr)
		}
		w.reported, w.lastOK = true, ok
	}
	if ok {
		w.attempt = 0
		return healthyInterval
	}
	w.attempt++
	if !bs {
		w.reconnect(ctx)
	}
	w.reserve()
	return backoffDelay(w.attempt, minBackoff, maxBackoff)
}

// startConnectivityWatchdog runs the watchdog for workers until ctx ends.
func startConnectivityWatchdog(ctx context.Context, h host.Host) {
	go watchAutoRelayAddrs(ctx, h)
	bootstraps.refresh(h.ID())

	w := &connectivityWatchdog{
		haveBootstraps: func() bool {
			bootstraps.refreshIfStale(h.ID())
			return !bootstraps.empty()
		},
		bootstrapConnected: func() bool { return bootstraps.connected(h) },
		relayReserved:      func() bool { return advertisedRelayValid(h) },
		reconnect: func(ctx context.Context) {
			tryReconnectToBootstraps(ctx, h, 10*time.Second)
		},
		reserve: MakeRelayReservations,
	}
	// Give the startup path (CRDT + the 10 s reservation) a chance first.
	if !waitFor(ctx, 15*time.Second) {
		return
	}
	for {
		if !waitFor(ctx, w.step(ctx)) {
			return
		}
	}
}
