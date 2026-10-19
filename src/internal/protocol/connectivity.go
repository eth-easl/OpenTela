package protocol

import (
	"context"
	"sync"
	"time"

	"opentela/internal/common"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/viper"
)

// A worker (no public-addr) is only routable from the head if it holds a live
// connection to a bootstrap peer AND a live relay reservation on it. Both are
// established once at startup (the reservation 10 s after the CRDT store
// starts); if either attempt races the network coming up, the node stays
// healthy locally yet unreachable. startAutoReconnect only acts when there are
// zero connections and Reconnect() fires with 50% probability per minute, so
// recovery was slow and unreliable. connectivityWatchdog closes that gap by
// retrying until both conditions hold.

var relayReservation struct {
	mu      sync.Mutex
	expires time.Time
}

// setRelayReservation records the expiry of the latest successful reservation.
func setRelayReservation(expires time.Time) {
	relayReservation.mu.Lock()
	defer relayReservation.mu.Unlock()
	if expires.After(relayReservation.expires) {
		relayReservation.expires = expires
	}
}

// HasRelayReservation reports whether a relay reservation made by this node is
// still valid (with a one minute safety margin).
func HasRelayReservation() bool {
	relayReservation.mu.Lock()
	defer relayReservation.mu.Unlock()
	return time.Now().Add(time.Minute).Before(relayReservation.expires)
}

// IsBootstrapConnected reports whether this node has a live connection to at
// least one configured bootstrap peer other than itself.
func IsBootstrapConnected() bool {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return false
	}
	return bootstrapConnected(h)
}

func bootstrapConnected(h host.Host) bool {
	addrs := getDefaultBootstrapPeers(nil, viper.GetString("mode"))
	infos, err := peer.AddrInfosFromP2pAddrs(addrs...)
	if err != nil {
		return false
	}
	for _, info := range infos {
		if info.ID != h.ID() && h.Network().Connectedness(info.ID) == network.Connected {
			return true
		}
	}
	return false
}

// connectivityWatchdog drives a node towards "bootstrap connected and relay
// reserved". All side effects are injected so step() is unit-testable.
type connectivityWatchdog struct {
	bootstrapConnected func() bool
	relayReserved      func() bool
	reconnect          func(ctx context.Context) // dial bootstraps
	reserve            func()                    // MakeRelayReservations

	attempt int
	lastOK  *bool
}

// step performs one check and returns the delay before the next one.
func (w *connectivityWatchdog) step(ctx context.Context) time.Duration {
	const (
		healthyInterval = 30 * time.Second
		minBackoff      = 5 * time.Second
		maxBackoff      = time.Minute
	)
	bs, rr := w.bootstrapConnected(), w.relayReserved()
	ok := bs && rr
	if w.lastOK == nil || *w.lastOK != ok {
		if ok {
			common.Logger.Info("Connectivity OK: bootstrap connected and relay reserved")
		} else {
			common.Logger.Warnf("Connectivity incomplete: bootstrap_connected=%v relay_reserved=%v; retrying", bs, rr)
		}
		w.lastOK = &ok
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
	w := &connectivityWatchdog{
		bootstrapConnected: func() bool { return bootstrapConnected(h) },
		relayReserved:      HasRelayReservation,
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
