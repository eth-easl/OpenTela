package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// linkServer mimics the control plane's deploy-key link endpoints: challenge
// issuance plus proof verification (derive the peer ID from the proven
// public key, verify the signature over the challenge message), then the
// instance bind.
func linkServerMock(t *testing.T, linkStatus int, linkBody string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		switch r.URL.Path {
		case "/internal/instances/link/challenges":
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer otd-") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req struct {
				PeerID string `json:"peer_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			nonce := "test-nonce-24-bytes-ok!"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challenge_id": "ch-1",
				"peer_id":      req.PeerID,
				"audience":     "api.opentela.ai/internal/instances/link",
				"nonce":        nonce,
				"issued_at":    time.Now().UTC(),
				"expires_at":   time.Now().UTC().Add(time.Minute),
				"message":      "opentela-instance-link-challenge\nchallenge_id=ch-1\npeer_id=" + req.PeerID + "\nregion=\nrole=\naudience=api.opentela.ai/internal/instances/link\nnonce=" + nonce + "\n",
			})
		case "/internal/instances/link":
			if linkStatus != http.StatusOK && linkStatus != http.StatusCreated {
				http.Error(w, linkBody, linkStatus)
				return
			}
			var req struct {
				PeerID      string `json:"peer_id"`
				ChallengeID string `json:"challenge_id"`
				PublicKey   string `json:"public_key"`
				Signature   string `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			pubRaw, err := base64.StdEncoding.DecodeString(req.PublicKey)
			if err != nil {
				http.Error(w, "invalid proof", http.StatusBadRequest)
				return
			}
			pub, err := crypto.UnmarshalPublicKey(pubRaw)
			if err != nil {
				http.Error(w, "invalid proof", http.StatusBadRequest)
				return
			}
			derived, err := peer.IDFromPublicKey(pub)
			if err != nil || derived.String() != req.PeerID {
				http.Error(w, "invalid proof", http.StatusBadRequest)
				return
			}
			sig, err := base64.StdEncoding.DecodeString(req.Signature)
			if err != nil {
				http.Error(w, "invalid proof", http.StatusBadRequest)
				return
			}
			// The mock re-derives the message exactly as the client signed it.
			ok, _ := pub.Verify([]byte("opentela-instance-link-challenge\nchallenge_id=ch-1\npeer_id="+req.PeerID+"\nregion=\nrole=\naudience=api.opentela.ai/internal/instances/link\nnonce=test-nonce-24-bytes-ok!\n"), sig)
			if !ok {
				http.Error(w, "invalid proof", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "peer_id": req.PeerID, "label": "gpu-box",
				"owner_wallet": "WALLET1", "mode": "restricted",
				"ownership_status": "active", "created_at": time.Now().UTC(),
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func testPeerSigner(t *testing.T) (string, PeerSigner) {
	t.Helper()
	priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 256)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		t.Fatalf("peer id: %v", err)
	}
	pubRaw, err := crypto.MarshalPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubRaw)
	return pid.String(), func(message []byte) (string, string, error) {
		sig, err := priv.Sign(message)
		if err != nil {
			return "", "", err
		}
		return pubB64, base64.StdEncoding.EncodeToString(sig), nil
	}
}

func TestLinkInstanceHappyPath(t *testing.T) {
	var seen []string
	srv := linkServerMock(t, http.StatusCreated, "", &seen)
	defer srv.Close()

	peerID, sign := testPeerSigner(t)
	client := &Client{BaseURL: srv.URL, DeployKey: "otd-test"}
	linked, err := client.LinkInstance(context.Background(), peerID, "gpu-box", sign)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if linked.ID != 42 || linked.PeerID != peerID || linked.OwnerWallet != "WALLET1" {
		t.Fatalf("linked: %+v", linked)
	}
	if len(seen) != 2 || seen[0] != "/internal/instances/link/challenges" || seen[1] != "/internal/instances/link" {
		t.Fatalf("call sequence: %v", seen)
	}
}

func TestLinkInstanceRequiresDeployKey(t *testing.T) {
	var seen []string
	srv := linkServerMock(t, http.StatusCreated, "", &seen)
	defer srv.Close()
	peerID, sign := testPeerSigner(t)
	client := &Client{BaseURL: srv.URL}
	if _, err := client.LinkInstance(context.Background(), peerID, "", sign); err == nil ||
		!strings.Contains(err.Error(), "deploy key") {
		t.Fatalf("err=%v, want a deploy-key guidance error", err)
	}
	if len(seen) != 0 {
		t.Fatalf("no request should have been made: %v", seen)
	}
}

func TestLinkInstancePropagatesAPIError(t *testing.T) {
	var seen []string
	srv := linkServerMock(t, http.StatusForbidden, "deploy key exhausted or expired", &seen)
	defer srv.Close()
	peerID, sign := testPeerSigner(t)
	client := &Client{BaseURL: srv.URL, DeployKey: "otd-test"}
	_, err := client.LinkInstance(context.Background(), peerID, "", sign)
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != http.StatusForbidden {
		t.Fatalf("err=%v, want *APIError 403", err)
	}
}
