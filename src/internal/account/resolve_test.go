package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// resolveServer builds an httptest server exposing the two GET endpoints the
// boot resolution reads, guarded by the same Bearer check as production.
func resolveServer(t *testing.T, wallets http.HandlerFunc, billing http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /manage/wallets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wallets(w, r)
	})
	mux.HandleFunc("GET /manage/billing", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		billing(w, r)
	})
	return httptest.NewServer(mux)
}

func TestGetAccount(t *testing.T) {
	srv := resolveServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"wallets": [
					{"id": 7, "wallet": "BAYy5vWksgzmgCsVdEXZ3RBw7rDd3bw5gYoTCJl4dvFS", "primary": true,
					 "created_at": "2025-01-01T00:00:00Z"}
				],
				"identity": {"email": "op@example.com", "email_verified": true, "max_age_seconds": 3600}
			}`))
		},
		func(w http.ResponseWriter, r *http.Request) { t.Error("billing must not be called") },
	)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt"}
	acct, err := client.GetAccount(context.Background())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if acct.Email != "op@example.com" || !acct.EmailVerified {
		t.Fatalf("identity not resolved: %+v", acct)
	}
	if len(acct.Wallets) != 1 || !acct.HasWallet("BAYy5vWksgzmgCsVdEXZ3RBw7rDd3bw5gYoTCJl4dvFS") {
		t.Fatalf("wallets not resolved: %+v", acct.Wallets)
	}
	if acct.HasWallet("other") {
		t.Fatal("HasWallet true for unknown pubkey")
	}
}

func TestGetAccountMissingIdentity(t *testing.T) {
	srv := resolveServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"wallets": [], "identity": {}}`))
		},
		nil,
	)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt"}
	if _, err := client.GetAccount(context.Background()); err == nil {
		t.Fatal("expected error for response without identity email")
	}
}

func TestGetBilling(t *testing.T) {
	srv := resolveServer(t,
		nil,
		func(w http.ResponseWriter, r *http.Request) {
			// rawInt64 amounts arrive as quoted strings on the wire.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"mode": "live",
				"balance": {"credit_raw": "5000000", "reserved_raw": "250000",
					"available_raw": "4750000", "updated_at": "2025-06-01T00:00:00Z"},
				"caps": {},
				"deposits": {"enabled": true},
				"withdrawals_enabled": false,
				"primary_linked_wallet": "BAYy5vWksgzmgCsVdEXZ3RBw7rDd3bw5gYoTCJl4dvFS"
			}`))
		},
	)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt"}
	state, err := client.GetBilling(context.Background())
	if err != nil {
		t.Fatalf("GetBilling: %v", err)
	}
	if state.Mode != "live" {
		t.Fatalf("mode = %q", state.Mode)
	}
	if state.AvailableCreditRaw != 4750000 {
		t.Fatalf("available credit = %d", int64(state.AvailableCreditRaw))
	}
	if state.PrimaryLinkedWallet != "BAYy5vWksgzmgCsVdEXZ3RBw7rDd3bw5gYoTCJl4dvFS" {
		t.Fatalf("primary wallet = %q", state.PrimaryLinkedWallet)
	}
}

func TestGetBillingOff(t *testing.T) {
	srv := resolveServer(t,
		nil,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mode": "off", "balance": {"credit_raw": "0", "reserved_raw": "0", "available_raw": "0"}}`))
		},
	)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt"}
	state, err := client.GetBilling(context.Background())
	if err != nil {
		t.Fatalf("GetBilling: %v", err)
	}
	if state.Mode != "off" || state.AvailableCreditRaw != 0 || state.PrimaryLinkedWallet != "" {
		t.Fatalf("off state mismatch: %+v", state)
	}
}

func TestRawInt64Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    RawInt64
		wantErr bool
	}{
		{"quoted string", `"123456789012345678"`, 123456789012345678, false},
		{"bare number", `42`, 42, false},
		{"empty string", `""`, 0, false},
		{"null", `null`, 0, false},
		{"garbage", `"abc"`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got RawInt64
			err := json.Unmarshal([]byte(tt.json), &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unmarshal(%s) error = %v, wantErr %v", tt.json, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("Unmarshal(%s) = %d, want %d", tt.json, got, tt.want)
			}
		})
	}
}

func TestGetAccountExpiredToken(t *testing.T) {
	srv := resolveServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		},
		nil,
	)
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "expired"}
	_, err := client.GetAccount(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("want 401 APIError, got %v", err)
	}
}

// Ensure the LinkedWallet shape stays wire-compatible (created_at decodes).
func TestLinkedWalletDecode(t *testing.T) {
	var linked LinkedWallet
	data, _ := json.Marshal(map[string]any{"id": 1, "wallet": "w", "primary": false, "created_at": time.Now().UTC()})
	if err := json.Unmarshal(data, &linked); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
