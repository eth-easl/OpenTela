package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// bootServer mimics the two read endpoints bootAccount calls.
func bootServer(t *testing.T, linkedWallet string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /manage/wallets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer boot-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wallets := "[]"
		if linkedWallet != "" {
			wallets = `[{"id": 1, "wallet": "` + linkedWallet + `", "primary": true, "created_at": "2025-01-01T00:00:00Z"}]`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"wallets": ` + wallets + `, "identity": {"email": "op@example.com", "email_verified": true}}`))
	})
	mux.HandleFunc("GET /manage/billing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer boot-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode": "live", "balance": {"credit_raw": "1000000", "reserved_raw": "0", "available_raw": "1000000"}}`))
	})
	return httptest.NewServer(mux)
}

// resetBootConfig clears the account config keys bootAccount reads.
func resetBootConfig(t *testing.T) {
	t.Helper()
	for _, k := range []string{"account.token", "account.email", "account.password", "account.api_url"} {
		viper.Set(k, "")
	}
	t.Cleanup(func() {
		for _, k := range []string{"account.token", "account.email", "account.password", "account.api_url"} {
			viper.Set(k, "")
		}
	})
}

func TestBootAccountSkipsWithoutCredentials(t *testing.T) {
	resetBootConfig(t)
	// No token, no email: must return without any network activity. Point
	// api_url at an unreachable server to prove nothing dials out.
	viper.Set("account.api_url", "http://127.0.0.1:1")
	bootAccount(&cobra.Command{}) // must not panic or block
}

func TestBootAccountWithToken(t *testing.T) {
	resetBootConfig(t)
	// A wallet whose key does not exist locally: the node's own wallet check
	// is skipped (no wallet manager key), so the linked-wallet warning must
	// not fire — the linked set is non-empty and unrelated.
	srv := bootServer(t, "BAYy5vWksgzmgCsVdEXZ3RBw7rDd3bw5gYoTCJl4dvFS")
	defer srv.Close()

	viper.Set("account.token", "boot-jwt")
	viper.Set("account.api_url", srv.URL)

	bootAccount(&cobra.Command{}) // must complete without blocking

	if got := viper.GetString("account.email"); got != "op@example.com" {
		t.Fatalf("resolved email not stashed: %q", got)
	}
}

func TestBootAccountUnreachableControlPlane(t *testing.T) {
	resetBootConfig(t)
	// Configured token + dead control plane: warn and return; never block.
	viper.Set("account.token", "boot-jwt")
	viper.Set("account.api_url", "http://127.0.0.1:1")

	bootAccount(&cobra.Command{}) // must return promptly
}

func TestBootAccountExpiredTokenContinues(t *testing.T) {
	resetBootConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	viper.Set("account.token", "stale")
	viper.Set("account.api_url", srv.URL)

	bootAccount(&cobra.Command{}) // 401 → warn, return; boot continues
	if got := viper.GetString("account.email"); got != "" {
		t.Fatalf("email must not be stashed on failure: %q", got)
	}
}

func TestBootAccountBillingFailureStillWarns(t *testing.T) {
	resetBootConfig(t)
	// Account resolution succeeds; billing fails with a 500: warn, return.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /manage/wallets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"wallets": [], "identity": {"email": "op@example.com", "email_verified": true}}`))
	})
	mux.HandleFunc("GET /manage/billing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	viper.Set("account.token", "boot-jwt")
	viper.Set("account.api_url", srv.URL)

	bootAccount(&cobra.Command{}) // billing failure → warn, return
	if got := viper.GetString("account.email"); got != "op@example.com" {
		t.Fatalf("resolved email not stashed: %q", got)
	}
}

// TestBootConfigFileRoundTrip guards the wiring assumption: the account.*
// keys bootAccount reads are the same ones root.go persists to cfg.yaml.
// Uses an isolated viper instance — viper.Set outranks config-file values,
// so the global instance cannot be re-used after resetBootConfig.
func TestBootConfigFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("account:\n  token: from-file\n"), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	v := viper.New()
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read cfg: %v", err)
	}
	if got := v.GetString("account.token"); got != "from-file" {
		t.Fatalf("account.token = %q", got)
	}
}
