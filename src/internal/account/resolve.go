// Boot-time account resolution: the two read endpoints a node needs when it
// comes online. Both are plain GETs against the control plane, authenticated
// with the same Neon Auth JWT used for wallet linking:
//
//	GET {api}/manage/wallets   → identity email + the account's linked wallets
//	GET {api}/manage/billing   → billing mode + spendable deposit credit
//
// `otela start` uses these to reconcile the node with the operator's cloud
// account before serving (see entry/cmd/boot.go).

package account

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RawInt64 is an unscaled integer amount exactly as the control plane
// serializes it: a JSON string (e.g. "1234567"), so µUSDC-scale values
// survive JavaScript's float64. Decoding tolerates a bare JSON number too.
type RawInt64 int64

func (v *RawInt64) UnmarshalJSON(data []byte) error {
	raw := string(data)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return err
		}
		raw = unquoted
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		*v = 0
		return nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("raw int64 %q: %w", raw, err)
	}
	*v = RawInt64(parsed)
	return nil
}

// Account is the control plane's view of the operator, resolved from
// GET /manage/wallets.
type Account struct {
	// Email is the account's console identity (from the verified JWT sub).
	Email string
	// EmailVerified mirrors the auth server's verification flag.
	EmailVerified bool
	// Wallets are the wallets linked to the account (usually zero or one;
	// the API enforces one wallet per account).
	Wallets []LinkedWallet
}

type accountListResponse struct {
	Wallets  []LinkedWallet `json:"wallets"`
	Identity struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	} `json:"identity"`
}

// GetAccount fetches the operator's identity and linked wallets.
// A 401 means the JWT is expired — re-sign-in and retry.
func (c *Client) GetAccount(ctx context.Context) (*Account, error) {
	var raw accountListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/manage/wallets", nil, &raw); err != nil {
		return nil, err
	}
	acct := &Account{
		Email:         raw.Identity.Email,
		EmailVerified: raw.Identity.EmailVerified,
		Wallets:       raw.Wallets,
	}
	if acct.Email == "" {
		return nil, fmt.Errorf("control plane returned no identity email")
	}
	return acct, nil
}

// HasWallet reports whether pubkey is among the account's linked wallets.
func (a *Account) HasWallet(pubkey string) bool {
	for _, w := range a.Wallets {
		if w.Wallet == pubkey {
			return true
		}
	}
	return false
}

// BillingState is the boot-relevant subset of GET /manage/billing.
type BillingState struct {
	// Mode is the account's billing mode: "off" until the operator opts in.
	Mode string
	// AvailableCreditRaw is the spendable deposit credit (credit minus
	// reserved), in raw µUSDC.
	AvailableCreditRaw RawInt64
	// PrimaryLinkedWallet is the account's linked wallet, nil when none.
	PrimaryLinkedWallet string
}

type billingStateResponse struct {
	Mode    string `json:"mode"`
	Balance struct {
		CreditRaw    RawInt64  `json:"credit_raw"`
		ReservedRaw  RawInt64  `json:"reserved_raw"`
		AvailableRaw RawInt64  `json:"available_raw"`
		UpdatedAt    time.Time `json:"updated_at"`
	} `json:"balance"`
	PrimaryLinkedWallet *string `json:"primary_linked_wallet"`
}

// GetBilling fetches the account's billing snapshot (mode + spendable credit).
func (c *Client) GetBilling(ctx context.Context) (*BillingState, error) {
	var raw billingStateResponse
	if err := c.doJSON(ctx, http.MethodGet, "/manage/billing", nil, &raw); err != nil {
		return nil, err
	}
	state := &BillingState{Mode: raw.Mode, AvailableCreditRaw: raw.Balance.AvailableRaw}
	if raw.PrimaryLinkedWallet != nil {
		state.PrimaryLinkedWallet = *raw.PrimaryLinkedWallet
	}
	return state, nil
}
