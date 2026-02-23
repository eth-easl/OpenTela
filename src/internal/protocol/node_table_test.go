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

	// 3. Verify it remains in the table with Status=LEFT so TombstoneManager can
	//    find it for deferred cleanup.
	got, err = GetPeerFromTable("peer-leaving")
	if err != nil {
		t.Fatalf("expected LEFT peer to still be in table, got error: %v", err)
	}
	if got.Status != LEFT {
		t.Fatalf("expected peer status LEFT, got %s", got.Status)
	}
	if got.Connected {
		t.Fatal("expected Connected=false for LEFT peer")
	}
}

func TestNodeLeaveAndRejoin(t *testing.T) {
	// 1. Peer joins
	p := Peer{ID: "peer-rejoin", PublicAddress: "10.0.0.2", Status: CONNECTED, Connected: true}
	b, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), b)

	got, err := GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected peer to be in table after join")
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected CONNECTED, got %s", got.Status)
	}

	// 2. Peer leaves
	p.Status = LEFT
	p.Connected = false
	bLeft, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), bLeft)

	got, err = GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected LEFT peer to remain in table")
	}
	if got.Status != LEFT {
		t.Fatalf("expected LEFT, got %s", got.Status)
	}

	// 3. Peer rejoins — a non-LEFT update overwrites the LEFT status
	p.Status = CONNECTED
	p.Connected = true
	p.PublicAddress = "10.0.0.3" // new address after rejoin
	bRejoin, _ := json.Marshal(p)
	UpdateNodeTableHook(ds.NewKey("peer-rejoin"), bRejoin)

	got, err = GetPeerFromTable("peer-rejoin")
	if err != nil {
		t.Fatalf("expected peer to be in table after rejoin, got error: %v", err)
	}
	if got.Status != CONNECTED {
		t.Fatalf("expected CONNECTED after rejoin, got %s", got.Status)
	}
	if !got.Connected {
		t.Fatal("expected Connected=true after rejoin")
	}
	if got.PublicAddress != "10.0.0.3" {
		t.Fatalf("expected updated public address 10.0.0.3, got %s", got.PublicAddress)
	}
}
