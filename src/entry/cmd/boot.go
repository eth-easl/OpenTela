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
//     email+password sign-in fetches a fresh JWT; with neither, a stored
//     deploy key (from `otela instance link`) re-confirms the instance link;
//  2. resolves the account (identity email + linked wallets) and checks that
//     this node's default wallet is linked — an unlinked node still runs,
//     but its peers cannot be claimed from the console;
//  3. surfaces the billing state (mode + spendable deposit credit).
//
// Best-effort by design: the node must boot standalone, so every failure is
// a prominent warning with the fix, never a boot error. The account link
// matters for claiming peers and the API market, not for mesh liveness.
// bootInstanceLink re-confirms this node's instance link using a stored
// deploy key: the node signs a fresh challenge with its libp2p identity and
// the control plane re-asserts the peer→account binding. Idempotent by
// contract (same-account re-links spend no key use and keep the label), so
// it is safe on every boot. Best-effort like the rest of bootAccount.
func bootInstanceLink(deployKey string) {
	peerID, signer, err := nodeLinkSigner()
	if err != nil {
		common.Logger.Warnf("Could not load the node identity for the deploy-key link check; continuing without an account link (%v)", err)
		return
	}

	baseURL := viper.GetString("account.api_url")
	if baseURL == "" {
		baseURL = account.DefaultAPIBaseURL
	}
	client := &account.Client{BaseURL: baseURL, DeployKey: deployKey}

	ctx, cancel := context.WithTimeout(context.Background(), bootAccountTimeout)
	defer cancel()

	linked, err := client.LinkInstance(ctx, peerID, "", signer)
	if err != nil {
		common.Logger.Warnf("Deploy-key link check failed; continuing without an account link (%v)", explainInstanceLinkError(err))
		return
	}
	name := linked.Label
	if name == "" {
		name = linked.PeerID
	}
	if linked.Relinked {
		common.Logger.Infof("Instance '%s' re-confirmed with your OpenTela Cloud account (id %d)", name, linked.ID)
	} else {
		common.Logger.Infof("Instance '%s' linked to your OpenTela Cloud account (id %d)", name, linked.ID)
	}
}

func bootAccount(cmd *cobra.Command) {
	token := viper.GetString("account.token")
	email := viper.GetString("account.email")
	if token == "" && email == "" {
		// No account credentials on the node — that is the point of deploy
		// keys. If one is stored (by `otela instance link` or
		// `otela start --deploy-key`), use it to re-confirm the link. The
		// re-link is idempotent and spends no use; revocation and expiry are
		// detected here and surface as warnings, never boot errors.
		if deployKey := resolveDeployKey(); deployKey != "" {
			bootInstanceLink(deployKey)
			return
		}
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
