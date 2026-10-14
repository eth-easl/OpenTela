package protocol

import (
	"context"
	"encoding/json"
	"math/rand"
	"opentela/internal/common"
	"opentela/internal/common/process"
	"os"
	"strings"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/jasonlvhit/gocron"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
)

// var verificationKey = "ocf-verification-key"
var verificationProb = 0.5

func StartTicker() {
	err := gocron.Every(1).Minute().Do(func() {
		if rand.Float64() < verificationProb {
			Reconnect()
		}
	})
	common.ReportError(err, "Error while creating verification ticker")
	err = gocron.Every(30).Second().Do(func() {
		host, _ := GetP2PNode(nil)
		peers := host.Peerstore().Peers()
		var alive = 0
		var disconnected = 0
		for _, peer_id := range peers {
			if peer_id == host.ID() {
				continue
			}
			p, err := GetPeerFromTable(peer_id.String())
			if err != nil {
				continue
			}
			// Active liveness check: ping the peer through whatever
			// transport is available (direct or relay circuit).
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ch := ping.Ping(ctx, host, peer_id)
			var reachable bool
			select {
			case res, ok := <-ch:
				reachable = ok && res.Error == nil
				if !reachable && ok && res.Error != nil {
					common.Logger.Debugf("Ping failed for peer %s: %v", peer_id, res.Error)
				}
			case <-ctx.Done():
			}
			cancel()
			if !reachable {
				p.Connected = false
				disconnected++
			} else {
				p.Connected = true
				alive++
				// Only a successful ping advances LastSeen. Bumping it on a
				// failed ping too made every entry look permanently fresh, so
				// the staleness sweep could never fire — see UpdateNodeTableHook.
				p.LastSeen = time.Now().Unix()
			}
			// Liveness updates go to the in-memory node table only.
			// Writing Connected/LastSeen to CRDT on every tick was the
			// main source of DAG bloat (~12k writes/day), causing fresh
			// nodes to spend minutes walking history before they could
			// discover peers. Structural changes (join, leave, service
			// registration) still go through CRDT.
			value, err := json.Marshal(p)
			if err == nil {
				UpdateNodeTableHook(ds.NewKey(peer_id.String()), value)
			} else {
				common.Logger.Error("Error while marshalling peer: ", peer_id.String(), err)
			}
		}
		// The loop above can only see peers still in the peerstore. A worker
		// behind a relay disappears from it entirely when its job dies, so it
		// would never be pinged again and its row would freeze as
		// connected:true forever. Probe those through their relay here, on the
		// same 30s cadence, so a dead worker is retired within one tick rather
		// than lingering until someone notices.
		probeUnreachableServicePeers()

		if !process.HealthCheck() {
			common.Logger.Error("Health check failed")
			os.Exit(1)
		}
		common.Logger.Debugf("Verification Summary: %d alive peers, %d unreachable peers", alive, disconnected)
	})
	common.ReportError(err, "Error while creating verification ticker")

	// Periodic maintenance: stale peer cleanup and resource monitoring.
	err = gocron.Every(2).Minutes().Do(func() {
		GetResourceManagerStats()

		connectedPeers := ConnectedPeers()
		allPeers := AllPeers()
		common.Logger.Debugf("Connection Summary: %d connected peers, %d total known peers",
			len(connectedPeers), len(allPeers))

		if len(connectedPeers) == 0 {
			common.Logger.Debugf("Low connection count detected: only %d connected peers", len(connectedPeers))
			Reconnect()
		}

		// Timer-based maintenance for peers the probe does not cover (no
		// service, so no relay reservation to ask about). Service-carrying
		// peers are handled by probeUnreachableServicePeers on the 30s tick;
		// passing probeUnknown here keeps this pass from acting on them, which
		// is the fail-safe direction.
		//
		// This used to skip every service-carrying peer outright, which is what
		// made dead LLM workers permanently un-evictable. See decideSweepAction.
		now := time.Now()
		selfID := ""
		if h, _ := GetP2PNode(nil); h != nil {
			selfID = h.ID().String()
		}
		for id, p := range *GetAllPeers() {
			// Never sweep our own row. Its LastSeen is only stamped at
			// startup — the liveness tick pings everyone except self — so a
			// service-less head (no ReannounceLocalServices heartbeats either)
			// would otherwise be marked disconnected after peerDisconnectAfter
			// and deleted after peerStaleAfter, silently removing itself from
			// /v1/dnt/bootstraps until the next restart.
			if selfID != "" && strings.Trim(id, "/") == selfID {
				continue
			}
			switch decideSweepAction(p, probeUnknown, now) {
			case sweepDelete:
				common.Logger.Debugf("Removing stale peer %s (last seen %v)", id, time.Unix(p.LastSeen, 0))
				DeleteNodeTableHook(ds.NewKey(id))
			case sweepMarkDisconnected:
				p.Connected = false
				if value, err := json.Marshal(p); err == nil {
					UpdateNodeTableHook(ds.NewKey(id), value)
				}
			case sweepKeep:
			}
		}
	})
	common.ReportError(err, "Error while creating resource monitoring and clean-up ticker")
	<-gocron.Start()
}
