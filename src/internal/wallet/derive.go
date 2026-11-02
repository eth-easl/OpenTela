package wallet

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"

	"github.com/mr-tron/base58"
)

// deriveKeyDomain separates this derivation from any future use of deploy-key
// material, and from deploy keys themselves (a raw "otd-..." key is never a
// valid seed input by accident).
const deriveKeyDomain = "opentela-identity-v1:"

// DeriveIdentityKeyFromDeployKey deterministically derives an Ed25519 keypair
// from a cloud deploy key ("otd-..."). Wallet-less nodes use the derived key
// to sign their identity attestation, so a node's attested identity is rooted
// in the same secret that authorizes its OpenTela Cloud link — no Solana
// wallet required. The derivation is stable: re-provisioning the same deploy
// key (e.g. after a cleanslate wipe) reproduces the same identity.
//
// The deploy key never appears in the published record: only the derived
// public key does, and recovering the key from the pubkey or from signatures
// is computationally infeasible.
func DeriveIdentityKeyFromDeployKey(deployKey string) (ed25519.PrivateKey, string, error) {
	if deployKey == "" {
		return nil, "", fmt.Errorf("deploy key is empty")
	}
	seed := sha256.Sum256([]byte(deriveKeyDomain + deployKey))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, "", fmt.Errorf("derived key has unexpected public type")
	}
	return priv, base58.Encode(pub), nil
}
