package cmd

import (
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"opentela/internal/protocol"
)

var peerIDCmd = &cobra.Command{
	Use:   "peer-id",
	Short: "Print the local libp2p PeerID",
	Long: `Print the PeerID of the persisted identity key (~/.config/opentela/keys/id).

Nodes started with --ephemeral-identity do not use that key: each process
announces a fresh in-memory identity, which only appears in its own logs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if viper.GetBool(protocol.EphemeralIdentityKey) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is set; nodes started with it announce a fresh per-process peer ID, not the one below\n", protocol.EphemeralIdentityEnv)
		}
		keyPath, err := protocol.ResolveKeyPath()
		if err != nil {
			return fmt.Errorf("resolve key path: %w", err)
		}
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("read key %s: %w", keyPath, err)
		}
		priv, err := crypto.UnmarshalPrivateKey(data)
		if err != nil {
			return fmt.Errorf("unmarshal key: %w", err)
		}
		pid, err := peer.IDFromPublicKey(priv.GetPublic())
		if err != nil {
			return fmt.Errorf("derive peer id: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), pid.String())
		return nil
	},
}
