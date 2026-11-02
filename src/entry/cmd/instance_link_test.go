package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"opentela/internal/protocol"
)

// resetLinkConfig isolates the viper keys and the on-disk deploy-key store
// that the link/boot flows touch. config_dir redirects DeployKeyPath and
// ResolveKeyPath into a temp dir so tests never touch the real node state.
func resetLinkConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	viper.Set("config_dir", dir)
	keys := []string{"instance.deploy_key", "instance.label", "account.api_url", "account.token", "account.email"}
	t.Cleanup(func() {
		viper.Set("config_dir", "")
		viper.Set("instance.auto_link", false)
		for _, k := range keys {
			viper.Set(k, "")
		}
	})
	_ = dir
}

func TestStoreAndLoadDeployKey(t *testing.T) {
	resetLinkConfig(t)

	if got := protocol.LoadDeployKey(); got != "" {
		t.Fatalf("LoadDeployKey with no stored key = %q, want empty", got)
	}
	if err := protocol.StoreDeployKey("otd-secret"); err != nil {
		t.Fatalf("store: %v", err)
	}
	path, err := protocol.DeployKeyPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("deploy key perms = %v, want 0600", info.Mode().Perm())
	}
	if got := protocol.LoadDeployKey(); got != "otd-secret" {
		t.Fatalf("round trip = %q, want otd-secret", got)
	}
}

func TestResolveDeployKeyPrecedence(t *testing.T) {
	resetLinkConfig(t)

	// Neither source: empty.
	if got := resolveDeployKey(); got != "" {
		t.Fatalf("empty state = %q, want empty", got)
	}
	// Stored file only.
	if err := protocol.StoreDeployKey("otd-from-file"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := resolveDeployKey(); got != "otd-from-file" {
		t.Fatalf("file fallback = %q", got)
	}
	// Viper (flag / env / cfg.yaml) outranks the file.
	viper.Set("instance.deploy_key", "otd-from-flag")
	if got := resolveDeployKey(); got != "otd-from-flag" {
		t.Fatalf("viper precedence = %q, want otd-from-flag", got)
	}
}

func TestNodeLinkSignerStableIdentity(t *testing.T) {
	resetLinkConfig(t)

	peerID1, signer1, err := nodeLinkSigner()
	if err != nil {
		t.Fatalf("first nodeLinkSigner: %v", err)
	}
	peerID2, _, err := nodeLinkSigner()
	if err != nil {
		t.Fatalf("second nodeLinkSigner: %v", err)
	}
	if peerID1 != peerID2 {
		t.Fatalf("peer IDs differ across calls: %s vs %s", peerID1, peerID2)
	}

	// The signer must produce a signature verifiable by the identity pubkey.
	pubB64, sigB64, err := signer1([]byte("probe-message"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	pubRaw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		t.Fatalf("pub b64: %v", err)
	}
	pub, err := crypto.UnmarshalPublicKey(pubRaw)
	if err != nil {
		t.Fatalf("unmarshal pub: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("sig b64: %v", err)
	}
	if ok, _ := pub.Verify([]byte("probe-message"), sig); !ok {
		t.Fatal("signature does not verify against the derived public key")
	}
}

// linkStubServer mimics the two control-plane endpoints enough for the CLI
// flow: a challenge carrying a message, and a bind returning a fixed body.
func linkStubServer(t *testing.T, linkStatus int, body string, calls *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Path)
		switch r.URL.Path {
		case "/internal/instances/link/challenges":
			if got := r.Header.Get("Authorization"); got != "Bearer otd-test" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"challenge_id": "ch-1",
				"peer_id":      "12D3KooWPHmsoT1AdLbLUzVDYTk3xx3jSPfFy3Y3FdPzYpbPLyrV",
				"audience":     "api.opentela.ai/internal/instances/link",
				"nonce":        "nonce-value-24-bytes-ok!!",
				"message":      "opentela-instance-link-challenge\nnonce=nonce-value-24-bytes-ok!!\n",
			})
		case "/internal/instances/link":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(linkStatus)
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunInstanceLinkPersistsKey(t *testing.T) {
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusOK,
		`{"id":9,"peer_id":"12D3KooWPHmsoT1AdLbLUzVDYTk3xx3jSPfFy3Y3FdPzYpbPLyrV","label":"gpu-box","mode":"restricted","ownership_status":"active","created_at":"2026-01-01T00:00:00Z"}`,
		&calls)
	defer srv.Close()

	viper.Set("account.api_url", srv.URL)
	viper.Set("instance.deploy_key", "otd-test")

	if err := runInstanceLink(&cobra.Command{}); err != nil {
		t.Fatalf("runInstanceLink: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("call sequence: %v", calls)
	}
	if got := protocol.LoadDeployKey(); got != "otd-test" {
		t.Fatalf("stored key = %q, want otd-test", got)
	}
}

func TestBootInstanceLinkReconfirms(t *testing.T) {
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusOK, `{"id":9,"peer_id":"p","label":"gpu-box","relinked":true}`, &calls)
	defer srv.Close()

	viper.Set("account.api_url", srv.URL)
	bootInstanceLink("otd-test") // must not panic and must hit both endpoints

	if len(calls) != 2 || calls[0] != "/internal/instances/link/challenges" || calls[1] != "/internal/instances/link" {
		t.Fatalf("call sequence: %v", calls)
	}
}

func TestBootInstanceLinkRevokedKeyContinues(t *testing.T) {
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusUnauthorized, "deploy key rejected", &calls)
	defer srv.Close()

	viper.Set("account.api_url", srv.URL)
	// A key the stub doesn't recognize is rejected at the challenge door —
	// exactly how the API treats revoked/unknown keys (401 before any
	// challenge is issued).
	bootInstanceLink("otd-revoked") // warning path; must return without panicking

	if len(calls) != 1 {
		t.Fatalf("expected only the challenge call, got %v", calls)
	}
}

func TestBootAccountPrefersDeployKeyWithoutAccountCreds(t *testing.T) {
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusOK, `{"id":9,"peer_id":"p","relinked":true}`, &calls)
	defer srv.Close()

	viper.Set("account.api_url", srv.URL)
	viper.Set("instance.deploy_key", "otd-test")
	viper.Set("instance.auto_link", true)

	bootAccount(&cobra.Command{}) // deploy-key branch: no account token/email needed

	if len(calls) != 2 {
		t.Fatalf("bootAccount should have used the deploy-key path, calls: %v", calls)
	}
}

func TestNodeLinkSignerUsesEphemeralIdentity(t *testing.T) {
	// With --ephemeral-identity the node serves with the in-memory key, so the
	// account link must name that peer and must not write a key file.
	resetLinkConfig(t)
	viper.Set(protocol.EphemeralIdentityKey, true)
	t.Cleanup(func() { viper.Set(protocol.EphemeralIdentityKey, false) })

	peerID, _, err := nodeLinkSigner()
	if err != nil {
		t.Fatalf("nodeLinkSigner: %v", err)
	}
	served, err := protocol.EphemeralKey()
	if err != nil {
		t.Fatalf("EphemeralKey: %v", err)
	}
	want, err := peerIDFromPriv(served)
	if err != nil {
		t.Fatalf("peerIDFromPriv: %v", err)
	}
	if peerID != want {
		t.Fatalf("linked peer %s, but the node serves as %s", peerID, want)
	}
	keyPath, err := protocol.ResolveKeyPath()
	if err != nil {
		t.Fatalf("ResolveKeyPath: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("a key file was written at %s (stat err %v)", keyPath, err)
	}
}

func TestNodeLinkSignerRefusesSeededIdentity(t *testing.T) {
	// A seed-derived key is guessable; it must never be linked to an account,
	// and the refusal must not mint a key file as a side effect.
	resetLinkConfig(t)
	viper.Set("seed", "7")
	t.Cleanup(func() { viper.Set("seed", "") })

	if _, _, err := nodeLinkSigner(); err == nil {
		t.Fatal("expected nodeLinkSigner to refuse a seeded identity")
	}
	keyPath, err := protocol.ResolveKeyPath()
	if err != nil {
		t.Fatalf("ResolveKeyPath: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("a key file was written at %s (stat err %v)", keyPath, err)
	}
}

func TestRunInstanceLinkRefusesEphemeralIdentity(t *testing.T) {
	// OF_EPHEMERAL_IDENTITY reaches every command; a standalone link would
	// spend a key use on an identity that dies with this process.
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusOK, `{"id":9,"peer_id":"p"}`, &calls)
	defer srv.Close()
	viper.Set("account.api_url", srv.URL)
	viper.Set("instance.deploy_key", "otd-test")
	viper.Set(protocol.EphemeralIdentityKey, true)
	t.Cleanup(func() { viper.Set(protocol.EphemeralIdentityKey, false) })

	if err := runInstanceLink(&cobra.Command{}); err == nil {
		t.Fatal("expected runInstanceLink to refuse an ephemeral identity")
	}
	if len(calls) != 0 {
		t.Fatalf("no control-plane call expected, got %v", calls)
	}
}

func TestNodeLinkSignerRejectsBadSeed(t *testing.T) {
	resetLinkConfig(t)
	viper.Set("seed", "not-a-number")
	t.Cleanup(func() { viper.Set("seed", "") })

	if _, _, err := nodeLinkSigner(); err == nil {
		t.Fatal("expected an error for a non-integer seed")
	}
}

func TestBootAccountSkipsDeployKeyLinkByDefault(t *testing.T) {
	resetLinkConfig(t)
	var calls []string
	srv := linkStubServer(t, http.StatusOK, `{"id":9,"peer_id":"p","relinked":true}`, &calls)
	defer srv.Close()

	viper.Set("account.api_url", srv.URL)
	viper.Set("instance.deploy_key", "otd-test")

	bootAccount(&cobra.Command{}) // auto-link off: a stored key alone must not link

	if len(calls) != 0 {
		t.Fatalf("bootAccount linked without auto-link, calls: %v", calls)
	}
}
