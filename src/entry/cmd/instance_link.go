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
  e.g. OF_INSTANCE_DEPLOY_KEY=otd-… otela instance link`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runInstanceLink(cmd); err != nil {
			fmt.Printf("Link failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func runInstanceLink(cmd *cobra.Command) error {
	// ── 1. The node's libp2p identity ────────────────────────────────
	priv, err := protocol.LoadKeyFromFile(), error(nil)
	if priv == nil {
		// First run on this machine: mint the same identity the node will
		// load on start, so linking and serving share one PeerID.
		priv, err = protocol.GenerateAndWriteKey()
		if err != nil {
			return fmt.Errorf("create node identity key: %w", err)
		}
	}
	peerID, err := peerIDFromPriv(priv)
	if err != nil {
		return err
	}
	pubRaw, err := crypto.MarshalPublicKey(priv.GetPublic())
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubRaw)
	sign := account.PeerSigner(func(message []byte) (string, string, error) {
		sig, err := priv.Sign(message)
		if err != nil {
			return "", "", err
		}
		return pubB64, base64.StdEncoding.EncodeToString(sig), nil
	})

	// ── 2. The deploy key ────────────────────────────────────────────
	deployKey := viper.GetString("instance.deploy_key")
	if deployKey == "" {
		return errors.New("no deploy key: pass --deploy-key otd-… (mint one in the " +
			"console under Account → Instances → Deploy keys, or set OF_INSTANCE_DEPLOY_KEY)")
	}

	// ── 3. Challenge → sign → link ───────────────────────────────────
	baseURL := viper.GetString("account.api_url")
	if baseURL == "" {
		baseURL = account.DefaultAPIBaseURL
	}
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
	_ = viper.BindPFlag("account.api_url", instanceLinkCmd.Flags().Lookup("api-url"))

	instanceCmd.AddCommand(instanceLinkCmd)
	rootcmd.AddCommand(instanceCmd)
}

var instanceCmd = &cobra.Command{
	Use:   "instance",
	Short: "Manage this node's registration with OpenTela Cloud",
}
