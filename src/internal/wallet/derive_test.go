package wallet

import (
	"crypto/ed25519"
	"testing"

	"github.com/mr-tron/base58"
)

func TestDeriveIdentityKeyDeterministic(t *testing.T) {
	const dk = "otd-d3f7a5a544f61e7db013681fcc778ebebad8b816cfd8d5c2"

	priv1, pub1, err := DeriveIdentityKeyFromDeployKey(dk)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	priv2, pub2, err := DeriveIdentityKeyFromDeployKey(dk)
	if err != nil {
		t.Fatalf("derive again: %v", err)
	}

	if pub1 != pub2 {
		t.Fatalf("derivation not deterministic: %q vs %q", pub1, pub2)
	}
	if string(priv1) != string(priv2) {
		t.Fatal("private keys differ for the same deploy key")
	}

	// Different deploy key must produce a different identity.
	_, pub3, err := DeriveIdentityKeyFromDeployKey(dk + "0")
	if err != nil {
		t.Fatalf("derive other: %v", err)
	}
	if pub1 == pub3 {
		t.Fatal("different deploy keys must derive different identities")
	}
}

func TestDeriveIdentityKeyEmptyDeployKey(t *testing.T) {
	if _, _, err := DeriveIdentityKeyFromDeployKey(""); err == nil {
		t.Fatal("empty deploy key must be rejected")
	}
}

func TestDeriveIdentityKeyPubkeyEncoding(t *testing.T) {
	const dk = "otd-abcdef"
	_, pub, err := DeriveIdentityKeyFromDeployKey(dk)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	raw, err := base58.Decode(pub)
	if err != nil {
		t.Fatalf("pubkey not base58: %v", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		t.Fatalf("pubkey size = %d, want %d", len(raw), ed25519.PublicKeySize)
	}
}

func TestDerivedIdentitySignsVerifiableAttestation(t *testing.T) {
	const dk = "otd-d3f7a5a544f61e7db013681fcc778ebebad8b816cfd8d5c2"
	const peerID = "QmYBtYxjkMWsE8tUz3TiKncjumYzEDtFmpHeiJchS5TdoV"

	priv, pub, err := DeriveIdentityKeyFromDeployKey(dk)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	att, err := SignIdentityWithKey(peerID, 1790794387, priv, pub)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if att.WalletPubkey != pub || att.PeerID != peerID {
		t.Fatalf("attestation fields mismatch: %+v", att)
	}
	if err := VerifyIdentity(att); err != nil {
		t.Fatalf("derived attestation must verify: %v", err)
	}

	// Tampering with the peer ID must break verification.
	att.PeerID = "QmTampered"
	if err := VerifyIdentity(att); err == nil {
		t.Fatal("tampered attestation must not verify")
	}
}
