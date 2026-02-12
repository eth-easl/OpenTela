package protocol

import (
	"context"
	"encoding/json"
	"math/rand"
	"ocf/internal/common"
	"ocf/internal/common/process"
	"os"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/jasonlvhit/gocron"
	"github.com/libp2p/go-libp2p/core/network"
	libpeer "github.com/libp2p/go-libp2p/core/peer"
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
		// updateMyself()
		var reconnected = 0
		var disconnected = 0
		for _, peer_id := range peers {
			// check if peer is still connected
			p, error := GetPeerFromTable(peer_id.String())
			if error == nil {
				if host.Network().Connectedness(peer_id) == network.Connected {
					p.Connected = true
				} else if peer_id != host.ID() && host.Network().Connectedness(peer_id) != network.Connected {
					// try to dial the peer, if cannot dial, then mark it as disconnected
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					addrInfo := libpeer.AddrInfo{ID: peer_id, Addrs: host.Peerstore().Addrs(peer_id)}
					if len(addrInfo.Addrs) == 0 {
						p.Connected = false
						disconnected++
					} else if err := host.Connect(ctx, addrInfo); err != nil {
						common.Logger.With("err", err).Warnf("Failed to dial peer %s; marking disconnected", peer_id)
						p.Connected = false
						disconnected++
					} else {
						// Successfully reconnected
						common.Logger.Infof("Reconnected to peer %s", peer_id)
						p.Connected = true
						reconnected++
					}
				}
				// update last seen timestamp
				p.LastSeen = time.Now().Unix()
				value, err := json.Marshal(p)
				if err == nil {
					UpdateNodeTableHook(ds.NewKey(peer_id.String()), value)
				} else {
					common.Logger.Error("Error while marshalling peer: ", peer_id.String(), err)
				}
			}
		}
		if !process.HealthCheck() {
			common.Logger.Error("Health check failed")
			// exit myself
			os.Exit(1)
		}
		common.Logger.Debugf("Verification Summary: %d un-reachable peers, %d re-connected peers", disconnected, reconnected)
	})
	common.ReportError(err, "Error while creating verification ticker")

	// Add resource monitoring every 2 minutes
	err = gocron.Every(2).Minutes().Do(func() {
		GetResourceManagerStats()

		// Also log current connection count for easy monitoring
		connectedPeers := ConnectedPeers()
		allPeers := AllPeers()
		common.Logger.Debugf("Connection Summary: %d connected peers, %d total known peers",
			len(connectedPeers), len(allPeers))

		// Log if we have very few connections (potential issue)
		if len(connectedPeers) == 0 {
			common.Logger.Warnf("Low connection count detected: only %d connected peers", len(connectedPeers))
			Reconnect()
			// best-effort re-announce our services after trying to reconnect
			ReannounceLocalServices()
		}

		// Cleanup: remove peers that have been disconnected for a long time
		// Define staleness threshold
		staleAfter := 10 * time.Minute
		table := *GetAllPeers()
		now := time.Now().Unix()
		for id, p := range table {
			if !p.Connected && p.LastSeen > 0 {
				if time.Unix(p.LastSeen, 0).Add(staleAfter).Before(time.Now()) {
					common.Logger.Warnf("Removing stale peer %s (last seen %v)", id, time.Unix(p.LastSeen, 0))
					DeleteNodeTableHook(ds.NewKey(id))
				}
			}
			// Also mark peers with very old LastSeen as disconnected
			if p.Connected && p.LastSeen > 0 && time.Unix(p.LastSeen, 0).Add(2*time.Minute).Before(time.Now()) {
				p.Connected = false
				value, err := json.Marshal(p)
				if err == nil {
					UpdateNodeTableHook(ds.NewKey(id), value)
				}
			}
			// If LastSeen is zero, initialize it now
			if p.LastSeen == 0 {
				p.LastSeen = now
				value, err := json.Marshal(p)
				if err == nil {
					UpdateNodeTableHook(ds.NewKey(id), value)
				}
			}
		}
	})
	common.ReportError(err, "Error while creating resource monitoring and clean-up ticker")
	<-gocron.Start()
}
