package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"opentela/internal/account"
	"opentela/internal/protocol"
)

// instanceLinkTimeout bounds the whole handshake; the challenge is
// single-use and expires two minutes after issue.
const instanceLinkTimeout = 30 * time.Second

var instanceLinkCmd = &cobra.Command{
	Use:   "link",
	Short: "Link this node to your OpenTela Cloud account with a deploy key",
	Long: `Link this node's libp2p identity to your OpenTela Cloud account using a
scoped deploy key — no account password, JWT, or wallet key involved.

Mint a deploy key in the console (Account → Instances → Deploy keys), then:

  otela instance link --deploy-key otd-…

The node signs a server-issued challenge with its libp2p identity key
(~/.config/opentela, the same key that forms its PeerID); the private key
never leaves this machine. A deploy key authorizes ONLY linking instances:
it is usage-capped, expires, and can be revoked in the console at any time.

Config equivalents (cfg.yaml or env, prefix OF_):
  instance.deploy_key, account.api_url
  e.g. OF_INSTANCE_DEPLOY_KEY=otd-… otela instance link

The key is saved to ~/.config/opentela/keys/deploy_key (0600) after a
successful link. Later 'otela start' runs do NOT re-link by default; pass
--auto-link (or set instance.auto_link: true / OF_INSTANCE_AUTO_LINK=true)
to re-confirm the link at every boot. Re-links of the same peer are free,
but every new peer ID spends a use.`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runInstanceLink(cmd); err != nil {
			fmt.Printf("Link failed: %v\n", err)
			os.Exit(1)
		}
	},
}

// nodeLinkSigner resolves the node's libp2p identity through
// protocol.NodeIdentity — the same resolver the libp2p host uses — and
// returns the derived PeerID plus a PeerSigner over that identity. Shared by
// `otela instance link` and the boot-time link check: linking and serving
// must present the same identity.
//
// A seeded identity is refused: its private key is derivable from a small
// integer, so anyone who guesses the seed could sign as this peer. Seeds are
// for tests and benchmarks, never for an account-linked node.
func nodeLinkSigner() (string, account.PeerSigner, error) {
	seed, err := protocol.ConfiguredSeed()
	if err != nil {
		return "", nil, err
	}
	if seed != 0 && !viper.GetBool(protocol.EphemeralIdentityKey) {
		return "", nil, fmt.Errorf("refusing to link a seeded identity (seed=%d): its key is derivable "+
			"from the seed and is for testing only; run without --seed to link this node", seed)
	}
	priv, err := protocol.NodeIdentity(seed)
	if err != nil {
		return "", nil, fmt.Errorf("load node identity: %w", err)
	}
	peerID, err := peerIDFromPriv(priv)
	if err != nil {
		return "", nil, err
	}
	pubRaw, err := crypto.MarshalPublicKey(priv.GetPublic())
	if err != nil {
		return "", nil, fmt.Errorf("marshal public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubRaw)
	signer := account.PeerSigner(func(message []byte) (string, string, error) {
		sig, err := priv.Sign(message)
		if err != nil {
			return "", "", err
		}
		return pubB64, base64.StdEncoding.EncodeToString(sig), nil
	})
	return peerID, signer, nil
}

// resolveDeployKey returns the deploy key with the documented precedence:
// --deploy-key flag / OF_INSTANCE_DEPLOY_KEY / cfg.yaml (all read via the
// "instance.deploy_key" viper key) first, then the file persisted by a
// previous `otela instance link`.
// resolveAPIBaseURL returns the control-plane origin: an explicitly passed
// --api-url wins, then the account.api_url viper key (OF_ACCOUNT_API_URL or
// cfg.yaml), then the production default. The explicit flag check is what
// keeps instance link independent of wallet_link's binding of the same key.
func resolveAPIBaseURL(cmd *cobra.Command) string {
	if cmd != nil {
		if f := cmd.Flags().Lookup("api-url"); f != nil && f.Changed {
			return f.Value.String()
		}
	}
	if v := viper.GetString("account.api_url"); v != "" {
		return v
	}
	return account.DefaultAPIBaseURL
}

func resolveDeployKey() string {
	if key := viper.GetString("instance.deploy_key"); key != "" {
		return key
	}
	return protocol.LoadDeployKey()
}

func runInstanceLink(cmd *cobra.Command) error {
	// ── 1. The node's libp2p identity ──────────────────────────────────
	// An ephemeral identity dies with this process, so linking it here would
	// spend a deploy-key use on a peer that never comes online. It can only
	// be linked by the node that serves with it, at boot.
	if viper.GetBool(protocol.EphemeralIdentityKey) {
		return fmt.Errorf("%s is set: an ephemeral identity lives only as long as its node "+
			"process, so it cannot be linked from here; unset it to link the persisted identity",
			protocol.EphemeralIdentityEnv)
	}
	peerID, sign, err := nodeLinkSigner()
	if err != nil {
		return err
	}

	// ── 2. The deploy key ──────────────────────────────────────────
	deployKey := resolveDeployKey()
	if deployKey == "" {
		return errors.New("no deploy key: pass --deploy-key otd-… (mint one in the " +
			"console under Account → Instances → Deploy keys, set " +
			"OF_INSTANCE_DEPLOY_KEY, or store one with a previous link)")
	}

	// ── 3. Challenge → sign → link ───────────────────────────────────
	baseURL := resolveAPIBaseURL(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), instanceLinkTimeout)
	defer cancel()

	client := &account.Client{BaseURL: baseURL, DeployKey: deployKey}
	linked, err := client.LinkInstance(ctx, peerID, viper.GetString("instance.label"), sign)
	if err != nil {
		return explainInstanceLinkError(err)
	}

	fmt.Println("✔ Node linked to your OpenTela Cloud account")
	fmt.Printf("  Peer ID:      %s\n", linked.PeerID)
	if linked.Label != "" {
		fmt.Printf("  Label:        %s\n", linked.Label)
	}
	if linked.Relinked {
		fmt.Println("  (already linked — nothing changed)")
	} else {
		fmt.Printf("  Instance ID:  %d\n", linked.ID)
	}

	// Persist the key so future `otela start` runs re-confirm the link
	// automatically (idempotent re-links spend no use).
	if err := protocol.StoreDeployKey(deployKey); err != nil {
		fmt.Printf("  (could not save the deploy key for auto-use at start: %v)\n", err)
	} else if keyPath, pathErr := protocol.DeployKeyPath(); pathErr == nil {
		fmt.Printf("  Key saved:    %s (future `otela start` runs link automatically)\n", keyPath)
	}

	fmt.Println("\nManage this node at:")
	fmt.Println("  https://cloud.opentela.ai/account")
	return nil
}

func peerIDFromPriv(priv crypto.PrivKey) (string, error) {
	pid, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		return "", fmt.Errorf("derive peer id: %w", err)
	}
	return pid.String(), nil
}

// explainInstanceLinkError turns the control-plane reply into
// operator-friendly text.
func explainInstanceLinkError(err error) error {
	var apiErr *account.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Status {
	case 401:
		return fmt.Errorf("deploy key rejected (%v); mint a fresh one in the console "+
			"(Account → Instances → Deploy keys)", apiErr)
	case 403:
		return fmt.Errorf("deploy key exhausted (%v); its use budget is spent — mint "+
			"another one", apiErr)
	case 409:
		return fmt.Errorf("%v; if another account already claimed this node, remove it "+
			"there first or reclaim it via the wallet flow", apiErr)
	case 422:
		return fmt.Errorf("your account has no wallet linked (%v); link a wallet in the "+
			"console first — billing settles through it", apiErr)
	default:
		return apiErr
	}
}

func init() {
	instanceLinkCmd.Flags().String("deploy-key", "", "deploy key minted in the console (OF_INSTANCE_DEPLOY_KEY)")
	instanceLinkCmd.Flags().String("label", "", "friendly label for the instance (OF_INSTANCE_LABEL)")
	instanceLinkCmd.Flags().String("api-url", account.DefaultAPIBaseURL, "control-plane base URL (OF_ACCOUNT_API_URL)")

	_ = viper.BindPFlag("instance.deploy_key", instanceLinkCmd.Flags().Lookup("deploy-key"))
	_ = viper.BindPFlag("instance.label", instanceLinkCmd.Flags().Lookup("label"))
	// NOTE: --api-url deliberately has no BindPFlag: wallet_link.go binds
	// "account.api_url" too, and a second binder on the same key shadows the
	// first (last init wins — this flag silently broke once already).
	// resolveAPIBaseURL reads this flag directly instead.

	instanceCmd.AddCommand(instanceLinkCmd)
	rootcmd.AddCommand(instanceCmd)
}

var instanceCmd = &cobra.Command{
	Use:   "instance",
	Short: "Manage this node's registration with OpenTela Cloud",
}
