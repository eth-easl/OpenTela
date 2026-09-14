package account

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mr-tron/base58"
)

func newKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// linkServer builds an httptest server mimicking the two API endpoints.
// challengeStatus makes /manage/wallets/challenges reply with that status;
// linkHandler decides what /manage/wallets does.
func linkServer(t *testing.T, challengeStatus int, linkHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/manage/wallets/challenges", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["wallet"] == "" {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if challengeStatus != http.StatusOK {
			http.Error(w, http.StatusText(challengeStatus), challengeStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(challengeResponse{
			ID:        "chal-1",
			Message:   "api.opentela.ai wallet-link v1\nsub:u1\nwallet:" + req["wallet"],
			ExpiresAt: time.Now().Add(5 * time.Minute).UTC(),
		})
	})
	mux.HandleFunc("/manage/wallets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-jwt" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		linkHandler(w, r)
	})
	return httptest.NewServer(mux)
}

func TestLinkWalletHappyPath(t *testing.T) {
	pub, priv := newKeypair(t)
	walletB58 := base58.Encode(pub)

	srv := linkServer(t, http.StatusOK, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeID string `json:"challenge_id"`
			Signature   string `json:"signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode link request: %v", err)
		}
		if req.ChallengeID != "chal-1" {
			t.Fatalf("challenge_id=%q, want chal-1", req.ChallengeID)
		}
		sigBytes, err := base58.Decode(req.Signature)
		if err != nil {
			t.Fatalf("signature is not base58: %v", err)
		}
		// The server verifies over the message it issued; the message embeds
		// the wallet so we can re-derive it here.
		msg := "api.opentela.ai wallet-link v1\nsub:u1\nwallet:" + walletB58
		if !ed25519.Verify(pub, []byte(msg), sigBytes) {
			t.Fatal("signature does not verify against the issued message")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 9, "wallet": walletB58, "primary": true, "created_at": time.Now().UTC(),
		})
	})
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt", HTTP: srv.Client()}
	linked, err := client.LinkWallet(context.Background(), walletB58, priv)
	if err != nil {
		t.Fatalf("LinkWallet: %v", err)
	}
	if linked.Wallet != walletB58 || !linked.Primary || linked.ID != 9 {
		t.Fatalf("linked=%+v, want wallet=%s primary=true id=9", linked, walletB58)
	}
}

func TestLinkWalletUnauthorized(t *testing.T) {
	_, priv := newKeypair(t)
	srv := linkServer(t, http.StatusOK, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	// No Bearer set → the fake control plane 401s on the challenge call.
	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := client.LinkWallet(context.Background(), base58.Encode(make([]byte, 32)), priv)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err=%v, want APIError 401", err)
	}
}

func TestLinkWalletPropagatesConflictBody(t *testing.T) {
	_, priv := newKeypair(t)
	srv := linkServer(t, http.StatusOK, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w,
			"account already has a linked wallet; one account operates a single wallet for all its peers",
			http.StatusConflict)
	})
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, Bearer: "test-jwt", HTTP: srv.Client()}
	_, err := client.LinkWallet(context.Background(), base58.Encode(make([]byte, 32)), priv)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err=%v, want APIError", err)
	}
	if apiErr.Status != http.StatusConflict {
		t.Fatalf("status=%d, want 409", apiErr.Status)
	}
	if apiErr.Body != "account already has a linked wallet; one account operates a single wallet for all its peers" {
		t.Fatalf("body=%q, want the server's conflict text verbatim", apiErr.Body)
	}
}

// authServer mimics Better Auth's real header behavior (verified against
// better-auth 1.4.18 with the jwt plugin, the stack Neon Auth runs): the
// set-auth-jwt header is attached ONLY to /get-session responses, never to
// /sign-in/email, which instead establishes an HttpOnly session cookie.
// opts, when set, intercept a request and reply for themselves.
func authServer(t *testing.T, getSessionJWT string, opts ...func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/sign-in/email", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "better-auth.session_token", Value: "opaque-session-token", Path: "/", HttpOnly: true,
		})
		_, _ = w.Write([]byte(`{"user":{"email":"op@example.com"}}`))
	})
	mux.HandleFunc("/get-session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("get-session: want GET, got %s", r.Method)
		}
		if r.Header.Get("Cookie") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if getSessionJWT != "" {
			w.Header().Set("set-auth-jwt", getSessionJWT)
		}
		_, _ = w.Write([]byte(`{"session":{},"user":{"email":"op@example.com"}}`))
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, opt := range opts {
			if opt(w, r) {
				return
			}
		}
		mux.ServeHTTP(w, r)
	}))
}

// TestSignInEmailExchangesCookieForJWT pins the real two-step flow: sign-in
// sets the session cookie (with no JWT header), and the JWT arrives only on
// the follow-up /get-session call carrying that cookie.
func TestSignInEmailExchangesCookieForJWT(t *testing.T) {
	var getSessionHadCookie bool
	srv := authServer(t, "header.payload.signature", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/get-session" {
			getSessionHadCookie = r.Header.Get("Cookie") != ""
		}
		return false
	})
	defer srv.Close()

	jwt, err := SignInEmail(context.Background(), srv.Client(), srv.URL, "op@example.com", "correct-horse-battery")
	if err != nil {
		t.Fatalf("SignInEmail: %v", err)
	}
	if jwt != "header.payload.signature" {
		t.Fatalf("jwt=%q, want the set-auth-jwt header from get-session", jwt)
	}
	if !getSessionHadCookie {
		t.Fatal("get-session was called without the sign-in session cookie")
	}
}

func TestSignInEmailSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"INVALID_EMAIL_OR_PASSWORD","message":"Invalid email or password"}`))
	}))
	defer srv.Close()

	_, err := SignInEmail(context.Background(), srv.Client(), srv.URL, "op@example.com", "wrong")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("err=%v, want APIError 401", err)
	}
	if apiErr.Body == "" {
		t.Fatal("APIError lost the server's error body")
	}
}

// A two-factor account gets a 200 sign-in response with no session; the CLI
// must explain that instead of hunting for a JWT.
func TestSignInEmailTwoFactorRedirect(t *testing.T) {
	srv := authServer(t, "header.payload.signature", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sign-in/email" {
			_, _ = w.Write([]byte(`{"twoFactorRedirect":true}`))
			return true
		}
		return false
	})
	defer srv.Close()

	_, err := SignInEmail(context.Background(), srv.Client(), srv.URL, "op@example.com", "correct-horse-battery")
	if err == nil || !strings.Contains(err.Error(), "two-factor") {
		t.Fatalf("err=%v, want a two-factor explanation", err)
	}
}

// 200 sign-in with no session cookie (and no two-factor flag) cannot be
// turned into a JWT; the error must say so.
func TestSignInEmailNoSessionCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"email":"op@example.com"}}`))
	}))
	defer srv.Close()

	_, err := SignInEmail(context.Background(), srv.Client(), srv.URL, "op@example.com", "correct-horse-battery")
	if err == nil || !strings.Contains(err.Error(), "no session cookie") {
		t.Fatalf("err=%v, want a missing-session-cookie error", err)
	}
}

// Server signs in but /get-session carries no set-auth-jwt (JWT plugin
// disabled or changed): report it rather than sending an empty bearer.
func TestSignInEmailMissingJWTOnGetSession(t *testing.T) {
	srv := authServer(t, "")
	defer srv.Close()

	_, err := SignInEmail(context.Background(), srv.Client(), srv.URL, "op@example.com", "correct-horse-battery")
	if err == nil {
		t.Fatal("expected an error when set-auth-jwt is absent on get-session")
	}
}
