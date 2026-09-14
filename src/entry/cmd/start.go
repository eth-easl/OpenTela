package cmd

import (
	"opentela/internal/common"
	"opentela/internal/ingest"
	"opentela/internal/protocol"
	"opentela/internal/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start listening for incoming connections",
	Run: func(cmd *cobra.Command, args []string) {
		// Reconcile with the operator's cloud account before anything starts:
		// ensure a live session, verify wallet linkage, surface billing state.
		// Best-effort; see bootAccount.
		bootAccount(cmd)

		component := viper.GetString("component")

		if component == "ingress" {
			// Start only the ingestion service
			ingest.Run()
			return
		}

		// check if cleanslate is set
		if viper.GetBool("cleanslate") {
			// clean slate, by removing the database
			common.Logger.Debug("Cleaning slate")
			protocol.ClearCRDTStore()
		}

		if component == "all" {
			// Start ingestion in a goroutine if running everything
			go ingest.Run()
		}

		server.StartServer()
	}}

func init() {
	startCmd.Flags().String("component", "server", "Component to start (server, ingress, all)")
	_ = viper.BindPFlag("component", startCmd.Flags().Lookup("component"))

	startCmd.Flags().String("ingest-url", "http://localhost:8081", "URL of the data ingestion service")
	_ = viper.BindPFlag("ingest.url", startCmd.Flags().Lookup("ingest-url"))
}
