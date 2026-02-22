package cmd

import (
	"fmt"
	"ocf/internal/wallet"
	"time"

	"github.com/spf13/cobra"
)

var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Wallet management commands",
}

var walletCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new Solana account managed by OCF",
	Run: func(cmd *cobra.Command, args []string) {
		wm, err := wallet.NewWalletManager()
		if err != nil {
			fmt.Printf("Failed to initialize wallet manager: %v\n", err)
			return
		}

		account, err := wm.AddSolanaAccount()
		if err != nil {
			fmt.Printf("Failed to create Solana account: %v\n", err)
			return
		}

		fmt.Printf("Created Solana account %s\n", account.PublicKey)
		fmt.Printf("Keypair stored at %s\n", account.FilePath)
		if len(wm.Accounts()) == 1 {
			fmt.Println("This account is set as the default wallet.")
		} else {
			fmt.Println("Use `ocf wallet list` to view all managed accounts.")
		}
	},
}

var walletListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed accounts",
	Run: func(cmd *cobra.Command, args []string) {
		wm, err := wallet.NewWalletManager()
		if err != nil {
			fmt.Printf("Failed to initialize wallet manager: %v\n", err)
			return
		}

		accounts := wm.Accounts()
		if len(accounts) == 0 {
			fmt.Println("No accounts managed by OCF. Run `ocf wallet create` to generate one.")
			return
		}

		for idx, account := range accounts {
			prefix := " "
			if idx == 0 {
				prefix = "*"
			}
			fmt.Printf("%s [%d] %s (%s)\n", prefix, idx, account.PublicKey, account.Type)
			fmt.Printf("    stored at: %s\n", account.FilePath)
			fmt.Printf("    created:   %s\n", account.CreatedAt.Format(time.RFC3339))
		}
	},
}

var walletInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show the default account information",
	Run: func(cmd *cobra.Command, args []string) {
		wm, err := wallet.NewWalletManager()
		if err != nil {
			fmt.Printf("Failed to initialize wallet manager: %v\n", err)
			return
		}

		account, err := wm.DefaultAccount()
		if err != nil {
			fmt.Println("No default account configured. Run `ocf wallet create` to create one.")
			return
		}

		fmt.Printf("Default account: %s (%s)\n", account.PublicKey, account.Type)
		fmt.Printf("Keypair stored at: %s\n", account.FilePath)
		fmt.Printf("Created at: %s\n", account.CreatedAt.Format(time.RFC3339))
	},
}

func init() {
	walletCmd.AddCommand(walletCreateCmd)
	walletCmd.AddCommand(walletListCmd)
	walletCmd.AddCommand(walletInfoCmd)
	// rootcmd.AddCommand(walletCmd)
}
