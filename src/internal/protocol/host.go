package protocol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand"
	"net"
	"opentela/internal/common"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-datastore"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	dualdht "github.com/libp2p/go-libp2p-kad-dht/dual"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	basichost "github.com/libp2p/go-libp2p/p2p/host/basic"
	libp2pyamux "github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	relayClient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	"github.com/libp2p/go-yamux/v5"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
)

var P2PNode *host.Host
var ddht *dualdht.DHT
var hostOnce sync.Once
var autoReconnectOnce sync.Once
var MyID string

// holePunchService holds our manually-constructed hole punch service so that
// callers can trigger DCUtR explicitly via DirectConnect. libp2p's built-in
// EnableHolePunching creates the service but does not expose it; constructing
// our own lets us retry direct-connection upgrades for the bench.
var holePunchService *holepunch.Service

const (
	Version = "v0.2.5"
)

func GetP2PNode(ds datastore.Batching) (host.Host, dualdht.DHT) {
	hostOnce.Do(func() {
		ctx := context.Background()
		var err error
		seed := viper.GetString("seed")
		// try to parse the seed as int64
		seedInt, err := strconv.ParseInt(seed, 10, 64)
		if err != nil {
			common.Logger.Error("Seed is not a valid int64 value: ", seed, " parse error: ", err)
			os.Exit(1)
		}
		host, err := newHost(ctx, seedInt, ds)
		if err != nil {
			common.Logger.Error("Error while creating P2P node: ", err)
			os.Exit(1)
		}
		MyID = host.ID().String()
		P2PNode = &host
	})
	return *P2PNode, *ddht
}

// registerPingProtocol enables the libp2p built-in /ipfs/ping/1.0.0 service
// on the given host. It is a separate function so it can be exercised in tests
// without standing up the full newHost pipeline.
func registerPingProtocol(h host.Host) {
	ping.NewPingService(h)
}

func newHost(ctx context.Context, seed int64, ds datastore.Batching) (host.Host, error) {
	var err error
	if err != nil {
		common.Logger.Error("Error while creating connection manager: ", err)
	}
	var priv crypto.PrivKey
	if seed == 0 {
		// seed=0 (default): load existing key from disk for stable identity.
		// If no key file exists yet, generate a random one and persist it.
		priv = LoadKeyFromFile()
		if priv == nil {
			common.Logger.Debug("seed=0: no existing key file, generating and persisting new identity")
			priv, err = GenerateAndWriteKey()
			if err != nil {
				return nil, err
			}
		} else {
			common.Logger.Debug("seed=0: loaded existing identity from disk")
		}
	} else {
		// seed!=0: use persisted key file for stable identity across restarts.
		// On first run, generate from seed and save; subsequent runs load from file.
		priv = LoadKeyFromFile()
		if priv == nil {
			common.Logger.Debugf("No existing key file, generating from seed=%d", seed)
			r := mrand.New(mrand.NewSource(seed))
			priv, _, err = crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
			if err != nil {
				return nil, err
			}
			WriteKeyToFile(priv)
		}
	}
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256([]byte(Version))
	keyHex := hex.EncodeToString(hash[:])

	var buf bytes.Buffer
	buf.WriteString("/key/swarm/psk/1.0.0/\n")
	buf.WriteString("/base16/\n")
	buf.WriteString(keyHex + "\n")

	// psk, err := pnet.DecodeV1PSK(bytes.NewReader(buf.Bytes()))
	// if err != nil {
	// 	panic(err)
	// }

	listenAddrs := []string{
		"/ip4/0.0.0.0/tcp/" + viper.GetString("tcpport"),
		"/ip4/0.0.0.0/udp/" + viper.GetString("udpport") + "/quic",
	}
	// Dedicated WebSocket port for Cloudflare-proxied connections.
	// If wsport is set, listen on a separate port for WS traffic.
	// Otherwise, WS shares the TCP port (may not work with all proxies).
	if wsPort := viper.GetString("wsport"); wsPort != "" {
		listenAddrs = append(listenAddrs, "/ip4/0.0.0.0/tcp/"+wsPort+"/ws")
		common.Logger.Infof("WebSocket listener on port %s", wsPort)
	} else {
		listenAddrs = append(listenAddrs, "/ip4/0.0.0.0/tcp/"+viper.GetString("tcpport")+"/ws")
	}

	opts := []libp2p.Option{
		libp2p.DefaultTransports,
		libp2p.Identity(priv),
		// libp2p.PrivateNetwork(psk),
		libp2p.ResourceManager(newResourceManager()),
		// Override the default yamux muxer with a larger initial stream
		// window. Default Initial=256 KiB caps single-stream throughput at
		// ~70 Mbps over a 30 ms RTT relay path; bumping to 8 MiB lifts the
		// BDP ceiling so 4 MiB+ probes don't bottleneck on flow control.
		libp2p.Muxer(libp2pyamux.ID, bigWindowYamuxMuxer()),
		// libp2p.ConnectionManager(connmgr),
		libp2p.NATPortMap(),
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.Security(noise.ID, noise.New),
		libp2p.EnableNATService(),
		libp2p.EnableRelay(),
		// Hole-punching is enabled below by manually constructing holepunch.Service
		// so we can call DirectConnect directly. EnableHolePunching would create
		// its own internal service but doesn't expose it.
		libp2p.EnableAutoNATv2(),
		// Relay-as-server: lift the libp2p defaults (128 KB / 2 min per circuit,
		// 128 reservations, 16 circuits/peer) so HTTP-over-circuit traffic is
		// not reset on the first echo response or 20-sample latency probe.
		libp2p.EnableRelayService(
			relayv2.WithResources(relayServiceResources()),
			relayv2.WithInfiniteLimits(),
		),
		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			ddht, err = newDHT(ctx, h, ds)
			return ddht, err
		}),
	}

	if publicAddr := strings.TrimSpace(viper.GetString("public-addr")); publicAddr != "" {
		announcedAddr, err := buildPublicTCPMultiaddr(publicAddr, viper.GetString("tcpport"))
		if err != nil {
			common.Logger.With("public_addr", publicAddr).Warnf("Invalid public-addr for libp2p announce: %v", err)
		} else {
			opts = append(opts, libp2p.AddrsFactory(func(current []multiaddr.Multiaddr) []multiaddr.Multiaddr {
				return appendUniqueMultiaddrs(current, announcedAddr)
			}))
		}
	}

	// relayHost is published after host creation so the AutoRelay peer-source
	// callback cannot observe a partially constructed libp2p host.
	var relayHostMu sync.RWMutex
	var relayHost host.Host
	// reachability mode: "auto" lets AutoNATv2 decide per-peer; "public" /
	// "private" force the libp2p hint. Default mirrors prior behavior:
	// public if public-addr is set, private otherwise.
	reachability := strings.ToLower(strings.TrimSpace(viper.GetString("reachability")))
	switch reachability {
	case "auto":
		// no force — let AutoNAT decide
	case "public":
		opts = append(opts, libp2p.ForceReachabilityPublic())
	case "private":
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	default:
		if viper.GetString("public-addr") != "" {
			opts = append(opts, libp2p.ForceReachabilityPublic())
		} else {
			opts = append(opts, libp2p.ForceReachabilityPrivate())
		}
	}
	if reachability != "public" && viper.GetString("public-addr") == "" {
		// AutoRelay discovers relay servers from connected peers and
		// maintains active reservations so other nodes can reach us
		// via /p2p/<relay>/p2p-circuit/p2p/<us>.
		//
		// Defaults reserve on only 2 relays (desiredRelays=2). With several
		// bootstrap relays available, two workers may pick disjoint subsets,
		// so neither can dial the other through circuit. Reserve on every
		// candidate the peer source returns so the reservation set across
		// workers is identical (full-mesh coverage of advertised circuits).
		opts = append(opts, libp2p.EnableAutoRelayWithPeerSource(
			func(ctx context.Context, numPeers int) <-chan peer.AddrInfo {
				ch := make(chan peer.AddrInfo, numPeers)
				go func() {
					defer close(ch)
					relayHostMu.RLock()
					h := relayHost
					relayHostMu.RUnlock()
					if h == nil {
						return
					}
					for _, p := range h.Network().Peers() {
						select {
						case ch <- peer.AddrInfo{ID: p, Addrs: h.Peerstore().Addrs(p)}:
						case <-ctx.Done():
							return
						}
					}
				}()
				return ch
			},
			autorelay.WithNumRelays(16),
			autorelay.WithMaxCandidates(32),
			autorelay.WithMinCandidates(1),
			autorelay.WithBootDelay(5*time.Second),
			autorelay.WithMinInterval(5*time.Second),
		))
	}

	host, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}
	registerPingProtocol(host)
	relayHostMu.Lock()
	relayHost = host
	relayHostMu.Unlock()

	// Create holepunch service manually so callers can trigger DirectConnect
	// (DCUtR) on demand. This requires access to BasicHost internals — namely
	// the IDService and AllAddrs callback — which the Host interface does not
	// expose. The cast is safe: libp2p.New always returns *basic.BasicHost.
	if bh, ok := host.(*basichost.BasicHost); ok {
		hps, hpErr := holepunch.NewService(host, bh.IDService(), bh.AllAddrs)
		if hpErr != nil {
			common.Logger.Warnf("hole-punch service init failed: %v", hpErr)
		} else {
			holePunchService = hps
		}
	} else {
		common.Logger.Warn("host is not *basichost.BasicHost; hole punching disabled")
	}

	// Log connection events for debugging
	host.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(n network.Network, c network.Conn) {
			// Log the negotiated security protocol for observability.
			secProto := "<unknown>"
			if cs := c.ConnState(); cs.Security != "" {
				secProto = string(cs.Security)
			}
			common.Logger.Debugf("Connected to peer: %s  security=%s  conns=%d", c.RemotePeer(), secProto, len(n.Conns()))
			// On (re)connections, re-announce local services — but skip until at
			// least one service is registered; an empty publication would race
			// with the real entry and take time to be overwritten on other nodes.
			if hasLocalServices() {
				go ReannounceLocalServices()
			}

			// Mark peer as connected in node table.
			// If the peer is unknown, create a minimal entry so it is tracked
			// immediately. The full record (with build attestation) will
			// overwrite this when the CRDT PutHook fires.
			go func(pid peer.ID) {
				if pid == host.ID() {
					return
				}
				p, err := GetPeerFromTable(pid.String())
				if err != nil {
					// Peer not yet in table — create a minimal entry.
					common.Logger.Debugf("Adding minimal entry for new peer [%s] on connect", pid.String())
					p = Peer{ID: pid.String()}
				} else {
					common.Logger.Debugf("Updating peer [%s] on connect", pid.String())
				}
				p.Connected = true
				p.LastSeen = time.Now().Unix()
				if b, e := json.Marshal(p); e == nil {
					UpdateNodeTableHook(datastore.NewKey(pid.String()), b)
				} else {
					common.Logger.Debug("Failed to marshal peer on connect: ", e)
				}
			}(c.RemotePeer())
		},
		DisconnectedF: func(n network.Network, c network.Conn) {
			common.Logger.Debugf("Disconnected from peer: %s  conns=%d", c.RemotePeer(), len(n.Conns()))
			// Mark peer as disconnected — only update existing peers.
			go func(pid peer.ID) {
				if pid == host.ID() {
					return
				}
				p, err := GetPeerFromTable(pid.String())
				if err != nil {
					common.Logger.Debugf("Ignoring disconnect for unknown peer [%s]", pid.String())
					return
				}
				p.Connected = false
				common.Logger.Debugf("Marking peer [%s] disconnected", pid.String())
				// keep LastSeen as last known good; do not bump here
				if b, e := json.Marshal(p); e == nil {
					UpdateNodeTableHook(datastore.NewKey(pid.String()), b)
				} else {
					common.Logger.Debug("Failed to marshal peer on disconnect: ", e)
				}
			}(c.RemotePeer())
		},
	})

	// NOTE: auto-reconnect is started later by StartAutoReconnect() after
	// bitswap/CRDT are initialized, so that connection notifications reach
	// bitswap's peer manager.

	return host, nil
}

// StartAutoReconnect launches the background auto-reconnector. Must be called
// AFTER bitswap/CRDT are initialized so that connection events reach bitswap.
// The provided context controls the lifetime of the auto-reconnect loop.
func StartAutoReconnect(ctx context.Context) {
	autoReconnectOnce.Do(func() {
		host, _ := GetP2PNode(nil)
		go startAutoReconnect(ctx, host)
	})
}

// startAutoReconnect periodically checks if we lost connectivity and attempts to reconnect to bootstraps with backoff.
func startAutoReconnect(ctx context.Context, h host.Host) {
	const (
		healthCheckInterval = 30 * time.Second
		minBackoff          = 5 * time.Second
		maxBackoff          = 2 * time.Minute
		dialTimeout         = 10 * time.Second
	)

	attempt := 0

	for {
		if ctx.Err() != nil {
			return
		}

		if len(h.Network().Conns()) == 0 {
			attempt++
			if attempt == 1 {
				common.Logger.Debug("No active P2P connections; attempting reconnect to bootstraps")
			} else {
				backoff := backoffDelay(attempt-1, minBackoff, maxBackoff)
				common.Logger.With("attempt", attempt).Debugf("Reconnect will retry after %s", backoff)
				if !waitFor(ctx, backoff) {
					return
				}
			}

			if tryReconnectToBootstraps(ctx, h, dialTimeout) {
				if attempt > 1 {
					common.Logger.Debugf("P2P connectivity restored after %d attempts", attempt)
				}
				attempt = 0
				if !waitFor(ctx, healthCheckInterval) {
					return
				}
				continue
			}

			// Failed attempt; loop and escalate backoff
			continue
		}

		if attempt > 0 {
			common.Logger.Debug("P2P connectivity restored")
			attempt = 0
		}

		if !waitFor(ctx, healthCheckInterval) {
			return
		}
	}
}

func tryReconnectToBootstraps(ctx context.Context, h host.Host, dialTimeout time.Duration) bool {
	mode := viper.GetString("mode")
	addrs := getDefaultBootstrapPeers(nil, mode)
	if len(addrs) == 0 {
		common.Logger.Debug("Reconnect attempt skipped: no bootstrap addresses configured")
		return false
	}

	peerInfos, err := peer.AddrInfosFromP2pAddrs(addrs...)
	if err != nil {
		common.Logger.Debug("Failed to parse bootstrap peers during reconnect: ", err)
		return false
	}

	successes := 0
	for _, info := range peerInfos {
		if info.ID == h.ID() {
			continue
		}

		if h.Network().Connectedness(info.ID) == network.Connected {
			successes++
			continue
		}

		if len(info.Addrs) == 0 {
			common.Logger.With("peer", info.ID).Debug("Bootstrap peer has no address; skipping")
			continue
		}

		connectCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		err := h.Connect(connectCtx, info)
		cancel()

		if err != nil {
			if isTransientNetworkError(err) {
				common.Logger.With("peer", info.ID).Debugf("Transient error connecting to bootstrap: %v", err)
			} else {
				common.Logger.With("peer", info.ID).Debugf("Failed to connect to bootstrap: %v", err)
			}
			continue
		}

		common.Logger.Debugf("Connected to bootstrap peer %s", info.ID)
		successes++
	}

	if successes > 0 {
		go Reconnect()
		return true
	}

	common.Logger.Debug("Reconnect attempt failed; no bootstrap peers reachable")
	return false
}

func waitFor(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func backoffDelay(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	base := backoffBaseDelay(attempt, minDelay, maxDelay)
	if base <= 0 {
		return minDelay
	}

	jitterMax := base / 3
	if jitterMax <= 0 {
		return base
	}

	randSrc := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(randSrc.Int63n(int64(jitterMax)))
	return base + jitter
}

func backoffBaseDelay(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	if attempt <= 1 {
		return minDelay
	}

	delay := minDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= maxDelay {
			return maxDelay
		}
	}

	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

func bigWindowYamuxMuxer() *libp2pyamux.Transport {
	cfg := yamux.DefaultConfig()
	cfg.InitialStreamWindowSize = 8 * 1024 * 1024
	cfg.MaxStreamWindowSize = 16 * 1024 * 1024
	cfg.LogOutput = io.Discard
	cfg.ReadBufSize = 0
	cfg.MaxIncomingStreams = math.MaxUint32
	return (*libp2pyamux.Transport)(cfg)
}

func relayServiceResources() relayv2.Resources {
	r := relayv2.DefaultResources()
	r.MaxReservations = 512
	r.MaxCircuits = 128
	r.MaxReservationsPerIP = 64
	r.MaxReservationsPerASN = 256
	return r
}

func newResourceManager() network.ResourceManager {
	// InfiniteLimits removes per-protocol / per-peer / system stream caps.
	// DefaultLimits.AutoScale() refuses concurrent outbound streams once a
	// single peer-protocol pair has ~128 in flight, which 502s legitimate
	// head→worker forwarding at moderate RPS on slow models.
	limiter := rcmgr.NewFixedLimiter(rcmgr.InfiniteLimits)
	rm, err := rcmgr.NewResourceManager(limiter)
	if err != nil {
		common.Logger.Errorf("Failed to create resource manager, falling back to null (NO RESOURCE LIMITS): %v", err)
		return &network.NullResourceManager{}
	}
	return rm
}

func newDHT(ctx context.Context, h host.Host, ds datastore.Batching) (*dualdht.DHT, error) {
	dhtOpts := []dualdht.Option{
		dualdht.DHTOption(dht.NamespacedValidator("pk", record.PublicKeyValidator{})),
		dualdht.DHTOption(dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()})),
		dualdht.DHTOption(dht.Concurrency(512)),
		dualdht.DHTOption(dht.Mode(dht.ModeAuto)),
	}
	if ds != nil {
		dhtOpts = append(dhtOpts, dualdht.DHTOption(dht.Datastore(ds)))
	}
	return dualdht.New(ctx, h, dhtOpts...)
}

// GetConnectedPeers returns the list of connected peers
func ConnectedPeers() []*peer.AddrInfo {
	var pinfos = []*peer.AddrInfo{}
	host, _ := GetP2PNode(nil)
	for _, p := range host.Peerstore().Peers() {
		// check if the peer is connected
		if host.Network().Connectedness(p) == network.Connected {
			pinfos = append(pinfos, &peer.AddrInfo{
				ID:    p,
				Addrs: host.Peerstore().Addrs(p),
			})
		}
	}
	return pinfos
}

func AllPeers() []*PeerWithStatus {
	var pinfos = []*PeerWithStatus{}
	host, _ := GetP2PNode(nil)
	for _, p := range host.Peerstore().Peers() {
		pinfos = append(pinfos, &PeerWithStatus{
			ID:            p.String(),
			Connectedness: host.Network().Connectedness(p).String(),
		})
	}
	return pinfos
}

// BuildBootstrapAddr constructs a multiaddr string for a bootstrap peer.
// If publicPort is empty, fallbackPort is used (for un-upgraded peers).
func BuildBootstrapAddr(publicAddr, publicPort, fallbackPort, peerID string) string {
	port := publicPort
	if port == "" {
		port = fallbackPort
	}
	addr, err := buildPublicTCPMultiaddr(publicAddr, port)
	if err != nil {
		return ""
	}
	return addr.String() + "/p2p/" + peerID
}

func buildPublicTCPMultiaddr(publicAddr, port string) (multiaddr.Multiaddr, error) {
	if strings.TrimSpace(port) == "" {
		return nil, errors.New("empty port")
	}

	host, protocol, err := publicAddrHostAndProtocol(publicAddr)
	if err != nil {
		return nil, err
	}

	return multiaddr.NewMultiaddr(fmt.Sprintf("/%s/%s/tcp/%s", protocol, host, port))
}

func publicAddrHostAndProtocol(publicAddr string) (string, string, error) {
	addr := strings.TrimSpace(publicAddr)
	if addr == "" {
		return "", "", errors.New("empty public address")
	}

	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	} else if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = strings.TrimPrefix(strings.TrimSuffix(addr, "]"), "[")
	}

	if ip := net.ParseIP(addr); ip != nil {
		if ip.To4() != nil {
			return addr, "ip4", nil
		}
		return addr, "ip6", nil
	}

	if strings.ContainsAny(addr, "/ \t") {
		return "", "", fmt.Errorf("unsupported public address %q", publicAddr)
	}

	return addr, "dns", nil
}

func appendUniqueMultiaddrs(base []multiaddr.Multiaddr, extra ...multiaddr.Multiaddr) []multiaddr.Multiaddr {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]multiaddr.Multiaddr, 0, len(base)+len(extra))

	for _, addr := range base {
		key := addr.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}

	for _, addr := range extra {
		if addr == nil {
			continue
		}
		key := addr.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}

	return out
}

const maxBootstrapAge int64 = 10 * 60 // 10 minutes

// isRecentRelayPeer returns true when p has the "relay" role and was seen
// within maxBootstrapAge seconds of now.  Extracted for unit-testability.
func isRecentRelayPeer(p Peer) bool {
	for _, r := range p.Role {
		if r == "relay" {
			return (time.Now().Unix() - p.LastSeen) < maxBootstrapAge
		}
	}
	return false
}

func ConnectedBootstraps() []string {
	var bootstraps = []string{}
	dnt := GetAllPeers()
	host, _ := GetP2PNode(nil)
	fallbackPort := viper.GetString("tcpport")
	wsDomain := viper.GetString("ws_domain") // e.g., "p2p.opentela.ai"
	for _, p := range *dnt {
		if p.PublicAddress != "" {
			pid, err := peer.Decode(p.ID)
			if err != nil {
				common.Logger.Debugf("Skipping peer %s: invalid peer ID: %v", p.ID, err)
				continue
			}
			connected := host.Network().Connectedness(pid) == network.Connected
			isSelf := host.ID() == pid
			isRecentRelay := isRecentRelayPeer(p)
			common.Logger.Debugf("Peer %s addr=%s port=%s connected=%v self=%v recentRelay=%v", p.ID, p.PublicAddress, p.PublicPort, connected, isSelf, isRecentRelay)
			if connected || isSelf || isRecentRelay {
				bootstrapAddr := BuildBootstrapAddr(p.PublicAddress, p.PublicPort, fallbackPort, p.ID)
				bootstraps = append(bootstraps, bootstrapAddr)
				// Also advertise WSS multiaddr via Cloudflare domain so
				// firewall-restricted nodes (e.g., JSC) can connect on port 443.
				// Emitted for all peers with a public address — Cloudflare
				// round-robins, so some attempts may hit the wrong origin
				// (peer ID mismatch), but libp2p retries and succeeds.
				if wsDomain != "" {
					wssAddr := "/dns4/" + wsDomain + "/tcp/443/wss/p2p/" + p.ID
					bootstraps = append(bootstraps, wssAddr)
				}
			}
		}
	}
	bootstraps = common.DeduplicateStrings(bootstraps)
	return bootstraps
}

// MakeRelayReservations reserves a slot on every connected peer's relay
// service. This is required for relay v2: without a reservation, other peers
// cannot connect to us through the relay (they get NO_RESERVATION).
// Only runs for workers (nodes without public-addr).
func MakeRelayReservations() {
	if viper.GetString("public-addr") != "" {
		return // head/relay nodes don't need relay reservations
	}
	h, _ := GetP2PNode(nil)
	if h == nil {
		common.Logger.Debug("MakeRelayReservations: host not ready, skipping")
		return
	}
	var reservedRelay string
	for _, p := range h.Network().Peers() {
		if p == h.ID() {
			continue
		}
		ai := peer.AddrInfo{ID: p, Addrs: h.Peerstore().Addrs(p)}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := relayClient.Reserve(ctx, h, ai)
		cancel()
		if err != nil {
			common.Logger.Debugf("Relay reservation on %s failed: %v", p.String()[:12], err)
		} else {
			common.Logger.Infof("Relay reservation on %s succeeded", p.String()[:12])
			if reservedRelay == "" {
				reservedRelay = p.String()
			}
		}
	}
	// Store the relay peer ID in our own CRDT entry so head nodes
	// know which relay to route through to reach us.
	if reservedRelay != "" && GetSelf().RelayPeer != reservedRelay {
		SetMyselfRelayPeer(reservedRelay)
		ReannounceLocalServices()
		common.Logger.Infof("Registered relay peer %s in CRDT", reservedRelay[:12])
	}
}

// TryHolePunch invokes DCUtR (Direct Connection Upgrade through Relay) for the
// given peer. Returns nil on success (a direct connection now exists), or an
// error if the punch failed. Requires the holepunch service to be initialized.
func TryHolePunch(targetPeerID string) error {
	if holePunchService == nil {
		return errors.New("hole punch service not initialized")
	}
	pid, err := peer.Decode(targetPeerID)
	if err != nil {
		return fmt.Errorf("invalid peer id: %w", err)
	}
	return holePunchService.DirectConnect(pid)
}

// ConnectednessOf reports the libp2p connectedness state for the given peer:
// "Connected" (direct), "Limited" (circuit-only), "NotConnected", "CanConnect",
// "CannotConnect", or "" if the local host is not initialized.
func ConnectednessOf(targetPeerID string) string {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return ""
	}
	pid, err := peer.Decode(targetPeerID)
	if err != nil {
		return ""
	}
	return h.Network().Connectedness(pid).String()
}

// IsDirectlyConnected returns true if we have a direct libp2p connection
// to the given peer (not via relay circuit).
func IsDirectlyConnected(targetPeerID string) bool {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return false
	}
	pid, err := peer.Decode(targetPeerID)
	if err != nil {
		return false
	}
	return h.Network().Connectedness(pid) == network.Connected
}

func IsTrustedDirectConn(limited bool, remoteAddr multiaddr.Multiaddr) bool {
	if limited || remoteAddr == nil {
		return false
	}
	if _, err := remoteAddr.ValueForProtocol(multiaddr.P_CIRCUIT); err == nil {
		return false
	}
	return true
}

// HasTrustedDirectConnection returns true only when at least one live
// connection to the target is a non-limited, non-circuit direct path.
func HasTrustedDirectConnection(targetPeerID string) bool {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return false
	}
	pid, err := peer.Decode(targetPeerID)
	if err != nil {
		return false
	}
	for _, conn := range h.Network().ConnsToPeer(pid) {
		if IsTrustedDirectConn(conn.Stat().Limited, conn.RemoteMultiaddr()) {
			return true
		}
	}
	return false
}

// FindRelayFor returns the peer ID of a connected peer that can relay
// requests to the target worker. It checks the worker's RelayPeer field
// first (the worker advertises which relay it reserved a slot on), then
// falls back to any connected relay-role peer.
func FindRelayFor(targetPeerID string) string {
	h, _ := GetP2PNode(nil)
	if h == nil {
		return ""
	}

	// Best option: the worker advertised its relay in the CRDT.
	if targetInfo, err := GetPeerFromTable(targetPeerID); err == nil && targetInfo.RelayPeer != "" {
		relayPID, err := peer.Decode(targetInfo.RelayPeer)
		if err == nil && h.Network().Connectedness(relayPID) == network.Connected {
			common.Logger.Debugf("Using worker's advertised relay %s", targetInfo.RelayPeer[:12])
			return targetInfo.RelayPeer
		}
	}

	// Fallback: any connected peer with relay or head role that is also
	// connected to the target (i.e. can forward on our behalf).
	targetPID, err := peer.Decode(targetPeerID)
	if err != nil {
		return ""
	}
	for _, p := range h.Network().Peers() {
		if p == targetPID || p == h.ID() {
			continue
		}
		if peerInfo, err := GetPeerFromTable(p.String()); err == nil {
			for _, r := range peerInfo.Role {
				if r == "relay" || r == "head" {
					return p.String()
				}
			}
		}
	}
	return ""
}

// GetResourceManagerStats returns current resource usage statistics
func GetResourceManagerStats() {
	host, _ := GetP2PNode(nil)
	if rm := host.Network().ResourceManager(); rm != nil {
		// Try to get stats if available
		if statsGetter, ok := rm.(interface {
			Stat() rcmgr.ResourceManagerStat
		}); ok {
			stats := statsGetter.Stat()
			common.Logger.Debugf("Resource Manager: conns=%d (in:%d out:%d) streams=%d (in:%d out:%d) mem=%d",
				stats.System.NumConnsInbound+stats.System.NumConnsOutbound,
				stats.System.NumConnsInbound,
				stats.System.NumConnsOutbound,
				stats.System.NumStreamsInbound+stats.System.NumStreamsOutbound,
				stats.System.NumStreamsInbound,
				stats.System.NumStreamsOutbound,
				stats.System.Memory,
			)
		} else {
			common.Logger.Debug("Resource Manager present but stats not available")
		}
	} else {
		common.Logger.Debug("No Resource Manager configured")
	}
}
