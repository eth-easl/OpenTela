package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"ocf/internal/common"
	"ocf/internal/platform"
	"ocf/internal/wallet"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/spf13/viper"
)

var dntOnce sync.Once
var myself Peer

const (
	CONNECTED    string = "connected"
	DISCONNECTED string = "disconnected"
	LEFT         string = "left"
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

// Peer is a single node in the network, as can be seen by the current node.
type Peer struct {
	ID                string              `json:"id"`
	Latency           int                 `json:"latency"` // in ms
	Privileged        bool                `json:"privileged"`
	Owner             string              `json:"owner"`
	CurrentOffering   []string            `json:"current_offering"`
	Role              []string            `json:"role"`
	Status            string              `json:"status"`
	AvailableOffering []string            `json:"available_offering"`
	Service           []Service           `json:"service"`
	LastSeen          int64               `json:"last_seen"`
	Version           string              `json:"version"`
	PublicAddress     string              `json:"public_address"`
	Hardware          common.HardwareSpec `json:"hardware"`
	Connected         bool                `json:"connected"`
	Load              []int               `json:"load"`
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
		// Preserve existing provider if not set in the update
		if peer.Owner == "" && existingPeer.Owner != "" {
			peer.Owner = existingPeer.Owner
		}
	}
	if viper.GetString("public-addr") != "" {
		peer.PublicAddress = viper.GetString("public-addr")
	}
	value, err := json.Marshal(peer)
	common.ReportError(err, "Error while marshalling peer")
	if err := store.Put(ctx, key, value); err != nil {
		common.Logger.Error("Error while updating node table: ", err)
	}
}

func MarkSelfAsBootstrap() {
	if viper.GetString("public-addr") != "" {
		common.Logger.Info("Registering myself as a bootstrap node")
		ctx := context.Background()
		store, _ := GetCRDTStore()
		host, _ := GetP2PNode(nil)
		key := ds.NewKey(host.ID().String())
		peer := Peer{
			ID:            host.ID().String(),
			PublicAddress: viper.GetString("public-addr"),
			Connected:     true,
		}
		value, err := json.Marshal(peer)
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
	common.Logger.Info("Announcing myself as LEFT from the network")

	// Update self status to LEFT
	myself.Status = LEFT
	myself.Connected = false
	myself.LastSeen = time.Now().Unix()

	value, err := json.Marshal(myself)
	if err != nil {
		common.Logger.Error("Error while marshalling peer for leave: ", err)
		return
	}

	if err := store.Put(ctx, key, value); err != nil {
		common.Logger.Error("Error while announcing leave: ", err)
	}
}

func UpdateNodeTableHook(key ds.Key, value []byte) {
	table := *getNodeTable()
	var peer Peer
	err := json.Unmarshal(value, &peer)
	common.ReportError(err, "Error while unmarshalling peer")

	// Check for Left status
	if peer.Status == LEFT {
		common.Logger.Debugf("Peer [%s] has left, removing from table", peer.ID)
		DeleteNodeTableHook(key)
		return
	}

	// Preserve locally computed connectivity status if we already know this peer
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	if existing, ok := table[key.String()]; ok {
		// If LastSeen is missing in the update, keep the existing one
		if peer.LastSeen == 0 {
			peer.LastSeen = existing.LastSeen
		}
	}
	// Always update LastSeen on any CRDT update we receive for that peer
	peer.LastSeen = time.Now().Unix()
	table[key.String()] = peer
}

func DeleteNodeTableHook(key ds.Key) {
	table := *getNodeTable()
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	delete(table, key.String())
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
	common.ReportError(err, "Error while getting peer")
	var peer Peer
	err = json.Unmarshal(value, &peer)
	common.ReportError(err, "Error while unmarshalling peer")
	for _, service := range peer.Service {
		if service.Name == name {
			return service, nil
		}
	}
	return Service{}, errors.New("Service not found")
}

func GetAllProviders(serviceName string) ([]Peer, error) {
	var providers []Peer
	table := *getNodeTable()
	tableUpdateSem <- struct{}{}
	defer func() { <-tableUpdateSem }() // Release on exit
	for _, peer := range table {
		if peer.Connected {
			for _, service := range peer.Service {
				if service.Name == serviceName {
					providers = append(providers, peer)
				}
			}
		}
	}
	if len(providers) == 0 {
		return providers, errors.New("no providers found")
	}
	return providers, nil
}

func InitializeMyself(ownerOverride string) {
	host, _ := GetP2PNode(nil)
	ctx := context.Background()
	store, _ := GetCRDTStore()
	key := ds.NewKey(host.ID().String())
	myself = Peer{
		ID:            host.ID().String(),
		PublicAddress: viper.GetString("public-addr"),
		LastSeen:      time.Now().Unix(),
		Connected:     true,
	}

	// Add wallet address as provider if available
	if ownerOverride != "" {
		myself.Owner = ownerOverride
		common.Logger.Infof("Using verified wallet account for provider: %s", myself.Owner)
	} else if account := viper.GetString("wallet.account"); account != "" {
		myself.Owner = account
		common.Logger.Infof("Using configured wallet account for provider: %s", myself.Owner)
	} else if wm, err := wallet.InitializeWallet(); err == nil && wm.WalletExists() {
		myself.Owner = wm.GetPublicKey()
		if myself.Owner != "" {
			common.Logger.Infof("Added wallet address as provider: %s", myself.Owner)
		}
	}

	myself.Hardware.GPUs = platform.GetGPUInfo()
	value, err := json.Marshal(myself)
	common.ReportError(err, "Error while marshalling peer")
	err = store.Put(ctx, key, value)
	if err != nil {
		common.Logger.Error("Error while initializing myself in the node table: ", err)
	}
}
