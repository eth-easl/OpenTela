package protocol

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetRelayReservations clears the package-global reservation state and
// restores it after the test.
func resetRelayReservations(t *testing.T) {
	t.Helper()
	relayReservations.mu.Lock()
	prevExp, prevAuto := relayReservations.expires, relayReservations.autoRelay
	relayReservations.expires = map[peer.ID]time.Time{}
	relayReservations.autoRelay = map[peer.ID]bool{}
	relayReservations.mu.Unlock()
	t.Cleanup(func() {
		relayReservations.mu.Lock()
		relayReservations.expires, relayReservations.autoRelay = prevExp, prevAuto
		relayReservations.mu.Unlock()
	})
}

func setViperForTest(t *testing.T, key string, value any) {
	t.Helper()
	prev, had := viper.Get(key), viper.IsSet(key)
	viper.Set(key, value)
	t.Cleanup(func() {
		if had {
			viper.Set(key, prev)
		} else {
			viper.Set(key, nil)
		}
	})
}

func setSelfRelayPeerForTest(t *testing.T, relay string) {
	t.Helper()
	prev := GetSelf()
	p := prev
	p.RelayPeer = relay
	SetMyselfForTest(p)
	t.Cleanup(func() { SetMyselfForTest(prev) })
}

func newConnectedPair(t *testing.T) (host.Host, host.Host) {
	t.Helper()
	a, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	b, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	require.NoError(t, a.Connect(context.Background(), peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}))
	return a, b
}

func mustPeerID(t *testing.T) peer.ID {
	t.Helper()
	h, err := libp2p.New(libp2p.NoListenAddrs)
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })
	return h.ID()
}

// --- watchdog step ---------------------------------------------------------

func newTestWatchdog(have, bs, rr *bool, reconnects, reserves *int) *connectivityWatchdog {
	return &connectivityWatchdog{
		haveBootstraps:     func() bool { return *have },
		bootstrapConnected: func() bool { return *bs },
		relayReserved:      func() bool { return *rr },
		reconnect:          func(context.Context) { *reconnects++ },
		reserve:            func() { *reserves++ },
	}
}

func TestConnectivityWatchdog_RetriesUntilBootstrapAndRelayHold(t *testing.T) {
	have, bs, rr := true, false, false
	var reconnects, reserves int
	w := newTestWatchdog(&have, &bs, &rr, &reconnects, &reserves)
	ctx := context.Background()

	// Isolated: both retried, short backoff (never the 30s healthy interval).
	d := w.step(ctx)
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 1, reserves)
	assert.Less(t, d, 30*time.Second)

	// Bootstrap back but no reservation: only the reservation is retried.
	bs = true
	w.step(ctx)
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 2, reserves)

	// Fully connected: no side effects, relaxed interval, backoff reset.
	rr = true
	assert.Equal(t, 30*time.Second, w.step(ctx))
	assert.Equal(t, 1, reconnects)
	assert.Equal(t, 2, reserves)
	assert.Equal(t, 0, w.attempt)

	// Reservation lapses or relay disconnects -> retried again.
	rr = false
	w.step(ctx)
	assert.Equal(t, 3, reserves)
}

func TestConnectivityWatchdog_NoBootstrapsBacksOffWithoutReserving(t *testing.T) {
	have, bs, rr := false, false, false
	var reconnects, reserves int
	w := newTestWatchdog(&have, &bs, &rr, &reconnects, &reserves)

	assert.Equal(t, time.Minute, w.step(context.Background()))
	assert.Equal(t, time.Minute, w.step(context.Background()))
	assert.Equal(t, 0, reserves, "must not spam reservations without bootstraps")
	assert.Equal(t, 2, reconnects, "should keep retrying discovery")
}

func TestConnectivityWatchdogEnabled(t *testing.T) {
	setViperForTest(t, "public-addr", "")
	setViperForTest(t, "reachability", "auto")
	setViperForTest(t, "mode", "")
	assert.True(t, connectivityWatchdogEnabled())

	viper.Set("mode", "standalone")
	assert.False(t, connectivityWatchdogEnabled())

	viper.Set("mode", "")
	viper.Set("reachability", " Public ")
	assert.False(t, connectivityWatchdogEnabled())

	viper.Set("reachability", "auto")
	viper.Set("public-addr", "1.2.3.4")
	assert.False(t, connectivityWatchdogEnabled())
}

// --- bootstrap cache -------------------------------------------------------

func TestParseBootstrapAddrInfos_SkipsBadEntriesAndSelf(t *testing.T) {
	self, other := mustPeerID(t), mustPeerID(t)
	addrs := []multiaddr.Multiaddr{
		multiaddr.StringCast("/ip4/10.0.0.1/tcp/43905"), // no /p2p component
		multiaddr.StringCast("/ip4/10.0.0.2/tcp/43905/p2p/" + other.String()),
		multiaddr.StringCast("/ip4/10.0.0.3/tcp/43905/p2p/" + other.String()),
		multiaddr.StringCast("/ip4/10.0.0.4/tcp/43905/p2p/" + self.String()),
	}
	got := parseBootstrapAddrInfos(addrs, self)
	require.Len(t, got, 1)
	assert.Len(t, got[other].Addrs, 2, "addresses for the same peer are merged")
}

func TestBootstrapCache_KeepsLastKnownSetOnEmptyRefresh(t *testing.T) {
	other := mustPeerID(t)
	c := &bootstrapCache{peers: map[peer.ID]peer.AddrInfo{other: {ID: other}}, at: time.Now()}

	setViperForTest(t, "mode", "standalone") // resolves to no peers
	c.refresh(mustPeerID(t))
	assert.True(t, c.contains(other))
	assert.False(t, c.empty())
}

func TestBootstrapCache_Connected(t *testing.T) {
	a, b := newConnectedPair(t)
	c := &bootstrapCache{peers: map[peer.ID]peer.AddrInfo{}}
	assert.False(t, c.connected(a))

	c.peers[mustPeerID(t)] = peer.AddrInfo{}
	assert.False(t, c.connected(a), "unconnected bootstrap does not count")

	c.peers[b.ID()] = peer.AddrInfo{ID: b.ID()}
	assert.True(t, c.connected(a))
}

// --- relay reservation state -----------------------------------------------

func TestCircuitRelayPeer(t *testing.T) {
	relay, target := mustPeerID(t), mustPeerID(t)
	p, ok := circuitRelayPeer(multiaddr.StringCast(
		"/ip4/10.0.0.1/tcp/43905/p2p/" + relay.String() + "/p2p-circuit/p2p/" + target.String()))
	require.True(t, ok)
	assert.Equal(t, relay, p)

	_, ok = circuitRelayPeer(multiaddr.StringCast("/ip4/10.0.0.1/tcp/43905/p2p/" + relay.String()))
	assert.False(t, ok)
}

func TestRelayValid_RequiresConnectionAndUnexpiredReservation(t *testing.T) {
	resetRelayReservations(t)
	a, b := newConnectedPair(t)

	assert.False(t, relayValid(a, b.ID()), "no reservation yet")

	recordRelayReservation(b.ID(), time.Now().Add(30*time.Second))
	assert.False(t, relayValid(a, b.ID()), "within safety margin counts as expired")

	recordRelayReservation(b.ID(), time.Now().Add(time.Hour))
	assert.True(t, relayValid(a, b.ID()))

	// A reservation on some other peer must not make b valid, and vice versa.
	stranger := mustPeerID(t)
	recordRelayReservation(stranger, time.Now().Add(time.Hour))
	assert.False(t, relayValid(a, stranger), "reserved but not connected")

	// Disconnect: the reservation no longer counts, and forgetting clears it.
	require.NoError(t, a.Network().ClosePeer(b.ID()))
	assert.False(t, relayValid(a, b.ID()))
	forgetRelayReservation(b.ID())
	relayReservations.mu.Lock()
	_, still := relayReservations.expires[b.ID()]
	relayReservations.mu.Unlock()
	assert.False(t, still)
}

func TestRelayValid_AutoRelayCircuitCounts(t *testing.T) {
	resetRelayReservations(t)
	a, b := newConnectedPair(t)

	setAutoRelayAddrs([]multiaddr.Multiaddr{multiaddr.StringCast(
		"/ip4/127.0.0.1/tcp/1/p2p/" + b.ID().String() + "/p2p-circuit")})
	assert.True(t, relayValid(a, b.ID()))

	setAutoRelayAddrs(nil)
	assert.False(t, relayValid(a, b.ID()))
}

func TestAdvertisedRelayValid_TiedToAdvertisedRelayPeer(t *testing.T) {
	resetRelayReservations(t)
	a, b := newConnectedPair(t)
	recordRelayReservation(b.ID(), time.Now().Add(time.Hour))

	setSelfRelayPeerForTest(t, "")
	assert.False(t, advertisedRelayValid(a), "nothing advertised")

	setSelfRelayPeerForTest(t, mustPeerID(t).String())
	assert.False(t, advertisedRelayValid(a), "reservation on b does not cover a different advertised relay")

	setSelfRelayPeerForTest(t, b.ID().String())
	assert.True(t, advertisedRelayValid(a))
}

func TestPickAdvertisedRelay(t *testing.T) {
	resetRelayReservations(t)
	a, b := newConnectedPair(t)
	x, y := mustPeerID(t), mustPeerID(t)

	prevCache := bootstraps
	bootstraps = &bootstrapCache{peers: map[peer.ID]peer.AddrInfo{y: {ID: y}}}
	t.Cleanup(func() { bootstraps = prevCache })

	// Current advertised relay still valid: keep it.
	recordRelayReservation(b.ID(), time.Now().Add(time.Hour))
	setSelfRelayPeerForTest(t, b.ID().String())
	assert.Equal(t, b.ID().String(), pickAdvertisedRelay(a, []peer.ID{x, y}))

	// Advertised relay invalid: prefer a bootstrap peer, then any.
	setSelfRelayPeerForTest(t, x.String())
	assert.Equal(t, y.String(), pickAdvertisedRelay(a, []peer.ID{x, y}))
	assert.Equal(t, x.String(), pickAdvertisedRelay(a, []peer.ID{x}))
	assert.Equal(t, "", pickAdvertisedRelay(a, nil))
}

// --- parallel reservations -------------------------------------------------

func newHostConnectedTo(t *testing.T, n int) (host.Host, []peer.ID) {
	t.Helper()
	a, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	var ids []peer.ID
	for i := 0; i < n; i++ {
		b, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = b.Close() })
		require.NoError(t, a.Connect(context.Background(), peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}))
		ids = append(ids, b.ID())
	}
	return a, ids
}

func TestRelayCandidates_PrefersBootstrapAndRelayCapablePeers(t *testing.T) {
	a, ids := newHostConnectedTo(t, 3)
	boot, relay, worker := ids[0], ids[1], ids[2]

	prevCache := bootstraps
	bootstraps = &bootstrapCache{peers: map[peer.ID]peer.AddrInfo{boot: {ID: boot}}}
	t.Cleanup(func() { bootstraps = prevCache })

	got := relayCandidates(a, func(p peer.ID) bool { return p == relay })
	assert.ElementsMatch(t, []peer.ID{boot, relay}, got)
	assert.NotContains(t, got, worker)

	// Nothing qualifies: fall back to every connected peer.
	bootstraps = &bootstrapCache{}
	got = relayCandidates(a, func(peer.ID) bool { return false })
	assert.ElementsMatch(t, []peer.ID{boot, relay, worker}, got)
}

func TestReserveOnRelays_ParallelBoundedAndOrdered(t *testing.T) {
	resetRelayReservations(t)
	cands := make([]peer.ID, 20)
	for i := range cands {
		cands[i] = mustPeerID(t)
	}
	fail := map[peer.ID]bool{cands[3]: true, cands[11]: true}

	var inFlight, peak atomic.Int32
	reserve := func(ctx context.Context, _ host.Host, p peer.ID) (time.Time, error) {
		n := inFlight.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		if fail[p] {
			return time.Time{}, errors.New("no reservation")
		}
		return time.Now().Add(time.Hour), nil
	}

	start := time.Now()
	got := reserveOnRelays(nil, cands, reserve)
	elapsed := time.Since(start)

	assert.LessOrEqual(t, int(peak.Load()), relayReserveParallelism, "concurrency must be bounded")
	assert.Greater(t, int(peak.Load()), 1, "reservations must run in parallel")
	assert.Less(t, elapsed, 20*50*time.Millisecond/2, "should be much faster than serial")

	var want []peer.ID
	for _, p := range cands {
		if !fail[p] {
			want = append(want, p)
		}
	}
	assert.Equal(t, want, got, "successes are returned in candidate order")

	relayReservations.mu.Lock()
	defer relayReservations.mu.Unlock()
	assert.Len(t, relayReservations.expires, len(want))
	assert.NotContains(t, relayReservations.expires, cands[3])
}
