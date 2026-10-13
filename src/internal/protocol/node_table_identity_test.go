package protocol

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"opentela/internal/wallet"
)

// setupDeployKeyDir points viper's config_dir at a temp dir with the given
// deploy key stored (or none when key is ""), mirroring the on-disk state of
// a deploy-key-linked node.
func setupDeployKeyDir(t *testing.T, key string) {
	t.Helper()
	dir := t.TempDir()
	viper.Set("config_dir", dir)
	t.Cleanup(func() { viper.Set("config_dir", "") })
	if key != "" {
		if err := StoreDeployKey(key); err != nil {
			t.Fatalf("store deploy key: %v", err)
		}
	}
}

func TestDeployKeyIdentityDerivesConsistentRecord(t *testing.T) {
	const (
		dk     = "otd-d3f7a5a544f61e7db013681fcc778ebebad8b816cfd8d5c2"
		peerID = "QmYBtYxjkMWsE8tUz3TiKncjumYzEDtFmpHeiJchS5TdoV"
	)
	setupDeployKeyDir(t, dk)

	owner, att, ok := deployKeyIdentity(peerID)
	if !ok {
		t.Fatal("deploy-key-linked node must produce an identity")
	}
	if owner == "" || att == nil {
		t.Fatalf("incomplete identity: owner=%q att=%v", owner, att)
	}
	// Cloud/peer verification requires Owner == attested pubkey.
	if att.WalletPubkey != owner {
		t.Fatalf("owner %q != attested pubkey %q", owner, att.WalletPubkey)
	}
	if att.PeerID != peerID {
		t.Fatalf("attested peer %q != %q", att.PeerID, peerID)
	}
	if err := wallet.VerifyIdentity(att); err != nil {
		t.Fatalf("attestation must verify: %v", err)
	}
	if att.Timestamp <= 0 || att.Timestamp > time.Now().Unix()+60 {
		t.Fatalf("implausible timestamp %d", att.Timestamp)
	}
}

func TestDeployKeyIdentityStandaloneNode(t *testing.T) {
	setupDeployKeyDir(t, "") // no deploy key stored

	owner, att, ok := deployKeyIdentity("QmTestPeer")
	if ok {
		t.Fatalf("standalone node must not derive an identity, got owner=%q att=%v", owner, att)
	}
}

func TestDeployKeyIdentityStableAcrossRuns(t *testing.T) {
	const dk = "otd-0123456789abcdef"
	setupDeployKeyDir(t, dk)

	owner1, att1, ok1 := deployKeyIdentity("QmPeerA")
	if !ok1 {
		t.Fatal("first derivation failed")
	}
	// Simulate a cleanslate wipe + re-provision of the same deploy key.
	setupDeployKeyDir(t, dk)
	owner2, att2, ok2 := deployKeyIdentity("QmPeerB")
	if !ok2 {
		t.Fatal("second derivation failed")
	}
	if owner1 != owner2 {
		t.Fatalf("identity not stable across re-provisioning: %q vs %q", owner1, owner2)
	}
	if att1.WalletPubkey != att2.WalletPubkey {
		t.Fatal("attested pubkey not stable across re-provisioning")
	}
}
