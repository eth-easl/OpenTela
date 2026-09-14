package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"opentela/internal/account"
	"opentela/internal/common"
	"opentela/internal/wallet"
)

// bootAccountTimeout bounds each control-plane round trip at boot. Sign-in
// can be slower than a plain GET, so the budget is generous but finite — a
// dead console must not stall node startup for minutes.
const bootAccountTimeout = 15 * time.Second

// billingModeOff is the control plane's "billing disabled" mode
// (api-side config.BillingOff).
const billingModeOff = "off"

// bootAccount reconciles the node with the OpenTela Cloud control plane just
// before the node starts serving. It:
//
//  1. ensures a live session — a configured token is used as-is, otherwise
//     email+password sign-in fetches a fresh JWT;
//  2. resolves the account (identity email + linked wallets) and checks that
//     this node's default wallet is linked — an unlinked node still runs,
//     but its peers cannot be claimed from the console;
//  3. surfaces the billing state (mode + spendable deposit credit).
//
// Best-effort by design: the node must boot standalone, so every failure is
// a prominent warning with the fix, never a boot error. The account link
// matters for claiming peers and the API market, not for mesh liveness.
func bootAccount(cmd *cobra.Command) {
	token := viper.GetString("account.token")
	email := viper.GetString("account.email")
	if token == "" && email == "" {
		common.Logger.Debug("No account credentials configured; skipping account resolution (standalone node)")
		return
	}

	if token == "" {
		var err error
		token, err = obtainJWT(cmd)
		if err != nil {
			common.Logger.Warnf("Account sign-in failed; continuing without an account link (%v)", err)
			return
		}
	}

	baseURL := viper.GetString("account.api_url")
	if baseURL == "" {
		baseURL = account.DefaultAPIBaseURL
	}
	client := &account.Client{BaseURL: baseURL, Bearer: token}

	ctx, cancel := context.WithTimeout(context.Background(), bootAccountTimeout)
	defer cancel()

	acct, err := client.GetAccount(ctx)
	if err != nil {
		common.Logger.Warnf("Could not resolve your OpenTela Cloud account; continuing without an account link (%v)", err)
		return
	}
	common.Logger.Infof("OpenTela Cloud account resolved: %s (%d linked wallet(s))", acct.Email, len(acct.Wallets))
	// Runtime-only: record the resolved identity so this session's logs and
	// any later caller of viper see the verified email, not just the
	// configured one.
	viper.Set("account.email", acct.Email)

	// Is this node's default wallet linked? Unlinked: the node runs, but the
	// console cannot claim its peers and it cannot earn as a seller.
	if wm, err := wallet.NewWalletManager(); err == nil {
		if pubkey := wm.GetPublicKey(); pubkey != "" {
			if acct.HasWallet(pubkey) {
				common.Logger.Debugf("Node wallet %s is linked to the account", pubkey)
			} else {
				common.Logger.Warnf(
					"Node wallet %s is NOT linked to your account; peers on this node cannot be claimed from the console — run `otela wallet link`",
					pubkey)
			}
		}
	}

	billing, err := client.GetBilling(ctx)
	if err != nil {
		common.Logger.Warnf("Could not read the billing state (%v)", err)
		return
	}
	if billing.Mode == billingModeOff {
		common.Logger.Info("Billing is off for your account (API market disabled)")
		return
	}
	common.Logger.Infof("Billing mode %s: spendable deposit credit %d (raw µUSDC)",
		billing.Mode, int64(billing.AvailableCreditRaw))
	if billing.AvailableCreditRaw <= 0 {
		common.Logger.Warnf(
			"No spendable deposit credit; API-market spending will fail until a deposit is credited (see cloud.opentela.ai/account)")
	}
}
