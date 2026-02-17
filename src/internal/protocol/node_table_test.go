package protocol

import (
	"encoding/json"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

func TestUpdateNodeTableHookAndGetPeer(t *testing.T) {
	_ = GetAllPeers()
	p := Peer{ID: "peer1", PublicAddress: "1.2.3.4"}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer1"), b)

	got, err := GetPeerFromTable("peer1")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.PublicAddress != "1.2.3.4" {
		t.Fatalf("unexpected peer: %+v", got)
	}
}

func TestDeleteNodeTableHook(t *testing.T) {
	table := GetAllPeers()
	p := Peer{ID: "peer2", PublicAddress: "5.6.7.8"}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer2"), b)
	DeleteNodeTableHook(ds.NewKey("peer2"))
	if _, ok := (*table)["/peer2"]; ok {
		t.Fatalf("expected peer2 deleted")
	}
}
func TestNodeLeave(t *testing.T) {
	// 1. Setup initial state
	p := Peer{ID: "peer-leaving", PublicAddress: "10.0.0.1", Status: CONNECTED, Connected: true}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-leaving"), b)

	// Verify it's in the table
	got, err := GetPeerFromTable("peer-leaving")
	if err != nil {
		t.Fatalf("expected peer to be in table")
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected peer status to be connected, got %s", got.Status)
	}

	// 2. Simulate Leave Update (Status = LEFT)
	p.Status = LEFT
	p.Connected = false
	bLeft, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-leaving"), bLeft)

	// 3. Verify it is removed from the table (since UpdateNodeTableHook calls DeleteNodeTableHook for LEFT nodes)
	_, err = GetPeerFromTable("peer-leaving")
	if err == nil {
		t.Fatalf("expected peer to be removed from table after leaving")
	}
}
