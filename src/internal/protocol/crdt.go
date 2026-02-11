package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"ocf/internal/common"
	"strings"
	"sync"
	"time"

	crdt "ocf/internal/protocol/go-ds-crdt"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	ds "github.com/ipfs/go-datastore"
	badger "github.com/ipfs/go-ds-badger"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/viper"
)

var (
	pubsubTopic = "ocf-crdt"
	pubsubKey   = "ocf-crdt"
	pubsubNet   = "ocf-crdt-net"
)
var ipfs *ipfslite.Peer
var crdtStore *crdt.Datastore
var once sync.Once
var cancelSubscriptions context.CancelFunc

func GetCRDTStore() (*crdt.Datastore, context.CancelFunc) {
	once.Do(func() {
		mode := viper.GetString("mode")
		host, dht := GetP2PNode(nil)
		ctx := context.Background()
		common.Logger.Info("Creating CRDT store, using dbpath: " + common.GetDBPath(host.ID().String()))
		store, err := badger.NewDatastore(common.GetDBPath(host.ID().String()), &badger.DefaultOptions)
		common.ReportError(err, "Error while creating datastore")

		ipfs, err = ipfslite.New(ctx, store, nil, host, &dht, nil)
		common.ReportError(err, "Error while creating ipfs lite node")
		pubsubParams := pubsub.DefaultGossipSubParams()
		pubsubParams.D = 128
		pubsubParams.Dlo = 16
		pubsubParams.Dhi = 256
		psub, err := pubsub.NewGossipSub(ctx, host, pubsub.WithGossipSubParams(pubsubParams))
		common.ReportError(err, "Error while creating pubsub")

		topic, err := psub.Join(pubsubNet)
		common.ReportError(err, "Error while joining pubsub topic")

		netSubs, err := topic.Subscribe()
		common.ReportError(err, "Error while subscribing to pubsub topic")

		go func() {
			for {
				msg, err := netSubs.Next(ctx)
				if err != nil {
					fmt.Println(err)
					break
				}
				host.ConnManager().TagPeer(msg.ReceivedFrom, "keep", 100)
				// Update LastSeen when we receive a message from a peer
				p, gerr := GetPeerFromTable(msg.ReceivedFrom.String())
				if gerr != nil {
					p = Peer{ID: msg.ReceivedFrom.String()}
					common.Logger.Debugf("Adding peer: [%s] triggered by msg received", msg.ReceivedFrom.String())
				} else {
					common.Logger.Debugf("Updating peer: [%s] triggered by msg received", msg.ReceivedFrom.String())
				}
				p.LastSeen = time.Now().Unix()
				p.Connected = true
				if b, merr := json.Marshal(p); merr == nil {
					UpdateNodeTableHook(ds.NewKey(msg.ReceivedFrom.String()), b)
				}
			}
		}()

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if err := topic.Publish(ctx, []byte("ping")); err != nil {
						common.Logger.Warn("Error while publishing ping: ", err)
					}
					time.Sleep(20 * time.Second)
				}
			}
		}()
		psubCtx, pcancel := context.WithCancel(ctx)
		cancelSubscriptions = pcancel
		pubsubBC, err := crdt.NewPubSubBroadcaster(psubCtx, psub, pubsubTopic)
		common.ReportError(err, "Error while creating pubsub broadcaster")
		opts := crdt.DefaultOptions()
		opts.Logger = common.Logger
		opts.RebroadcastInterval = 5 * time.Second
		opts.PutHook = func(k ds.Key, v []byte) {
			var peer Peer
			err := json.Unmarshal(v, &peer)
			common.ReportError(err, "Error while unmarshalling peer")
			// When a new peer is added to the table it is marked as diconnected by default.
			// Doing so allows to intercept ghost peers by the verification procedure.

			// Do not update itself
			host, _ := GetP2PNode(nil)
			if strings.Trim(k.String(), "/") == host.ID().String() {
				return
			}
			p, err := GetPeerFromTable(strings.Trim(k.String(), "/"))
			if err != nil {
				peer.Connected = false
				common.Logger.Debugf("Adding peer: [%s] triggered by p2p hook", strings.Trim(k.String(), "/"))
			} else {
				peer.Connected = p.Connected
				common.Logger.Debugf("Updating peer: [%s] triggered by p2p hook", strings.Trim(k.String(), "/"))
			}
			value, err := json.Marshal(peer)
			if err == nil {
				UpdateNodeTableHook(k, value)
			} else {
				common.Logger.Error("Error while marshalling peer", err)
			}
		}
		opts.DeleteHook = func(k ds.Key) {
			common.Logger.Debugf("Removed: [%s] triggered by p2p hook", strings.Trim(k.String(), "/"))
			DeleteNodeTableHook(k)
		}

		crdtStore, err = crdt.New(store, ds.NewKey(pubsubKey), ipfs, pubsubBC, opts)
		common.ReportError(err, "Error while creating crdt store")
		addsInfo, err := peer.AddrInfosFromP2pAddrs(getDefaultBootstrapPeers(nil, mode)...)
		common.ReportError(err, "Error while getting bootstrap peers")
		ipfs.Bootstrap(addsInfo)
		common.ReportError(err, "Error while starting ticker")
		// h.ConnManager().TagPeer(inf.ID, "keep", 100)
		common.Logger.Info("Mode: ", mode)
		common.Logger.Info("Peer ID: ", host.ID().String())
		common.Logger.Info("Listen Addr: ", host.Addrs())

		startTombstoneCompactor(crdtStore)
	})
	return crdtStore, cancelSubscriptions
}

func Reconnect() {
	mode := viper.GetString("mode")
	if ipfs == nil {
		common.Logger.Warn("Reconnect requested but CRDT/IPFS not initialized yet; skipping")
		return
	}
	addsInfo, err := peer.AddrInfosFromP2pAddrs(getDefaultBootstrapPeers(nil, mode)...)
	common.ReportError(err, "Error while getting bootstrap peers")
	ipfs.Bootstrap(addsInfo)
}

func ClearCRDTStore() {
	// remove ~/.ocfcore directory
	host, _ := GetP2PNode(nil)
	err := common.RemoveDir(common.GetDBPath(host.ID().String()))
	if err != nil {
		common.Logger.Error("Error while removing directory: ", err)
	}
}
