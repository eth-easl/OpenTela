package wallet

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/mr-tron/base58"
)

// IdentityAttestation is the signed message a node publishes to prove it
// controls a particular wallet key.  Other nodes verify the signature
// before granting trust.
type IdentityAttestation struct {
	// PeerID is the libp2p peer ID of the node.
	PeerID string `json:"peer_id"`
	// WalletPubkey is the Ed25519 public key (base58) of the operator wallet.
	WalletPubkey string `json:"wallet_pubkey"`
	// Timestamp is the Unix time when the attestation was created.
	Timestamp int64 `json:"timestamp"`
	// Signature is the base64-encoded Ed25519 signature over the canonical
	// JSON of {peer_id, wallet_pubkey, timestamp}.
	Signature string `json:"signature"`
}

// identityPayload is the message that gets signed (deterministic JSON).
type identityPayload struct {
	PeerID       string `json:"peer_id"`
	WalletPubkey string `json:"wallet_pubkey"`
	Timestamp    int64  `json:"timestamp"`
}

// SignIdentity creates a signed attestation binding a peer ID to a wallet.
func SignIdentity(peerID string, timestamp int64, wm *WalletManager) (*IdentityAttestation, error) {
	privKey, err := wm.GetPrivateKeyBytes()
	if err != nil {
		return nil, fmt.Errorf("cannot get private key for signing: %w", err)
	}
	pubKey := wm.GetPublicKey()
	if pubKey == "" {
		return nil, fmt.Errorf("no public key available")
	}
	return SignIdentityWithKey(peerID, timestamp, ed25519.PrivateKey(privKey), pubKey)
}

// SignIdentityWithKey creates a signed attestation binding a peer ID to the
// Ed25519 key held by privKey. pubkeyB58 must be the base58 encoding of
// privKey's public half; it is embedded as the attested WalletPubkey so the
// resulting record is byte-identical in shape to a wallet-signed one.
func SignIdentityWithKey(peerID string, timestamp int64, privKey ed25519.PrivateKey, pubkeyB58 string) (*IdentityAttestation, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid ed25519 private key size %d", len(privKey))
	}
	if pubkeyB58 == "" {
		return nil, fmt.Errorf("no public key available")
	}

	payload := identityPayload{
		PeerID:       peerID,
		WalletPubkey: pubkeyB58,
		Timestamp:    timestamp,
	}
	msg, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal identity payload: %w", err)
	}

	sig := ed25519.Sign(privKey, msg)

	return &IdentityAttestation{
		PeerID:       peerID,
		WalletPubkey: pubkeyB58,
		Timestamp:    timestamp,
		Signature:    base64.StdEncoding.EncodeToString(sig),
	}, nil
}

// VerifyIdentity checks that an identity attestation has a valid Ed25519
// signature from the claimed wallet public key.
func VerifyIdentity(att *IdentityAttestation) error {
	if att == nil {
		return fmt.Errorf("nil attestation")
	}

	// Decode the public key
	pubBytes, err := base58.Decode(att.WalletPubkey)
	if err != nil {
		return fmt.Errorf("invalid wallet pubkey encoding: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("wallet pubkey has wrong length: %d", len(pubBytes))
	}
	pubKey := ed25519.PublicKey(pubBytes)

	// Reconstruct the signed message
	payload := identityPayload{
		PeerID:       att.PeerID,
		WalletPubkey: att.WalletPubkey,
		Timestamp:    att.Timestamp,
	}
	msg, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal identity payload: %w", err)
	}

	// Decode and verify the signature
	sig, err := base64.StdEncoding.DecodeString(att.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}
	if !ed25519.Verify(pubKey, msg, sig) {
		return fmt.Errorf("identity signature verification failed for peer %s", att.PeerID)
	}
	return nil
}
