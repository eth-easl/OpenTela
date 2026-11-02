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
		// A --deploy-key passed at start is authoritative for this run and
		// links it: set the viper overrides so bootAccount uses it, and
		// persist the key so later boots can link with --auto-link (or
		// `otela instance link`) without the flag. Best-effort.
		if cmd.Flags().Changed("deploy-key") {
			key, _ := cmd.Flags().GetString("deploy-key")
			viper.Set("instance.deploy_key", key)
			viper.Set("instance.auto_link", true)
			if err := protocol.StoreDeployKey(key); err != nil {
				common.Logger.Warnf("Could not save the deploy key for auto-use on future boots (%v)", err)
			}
		}

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

	// NOTE: --deploy-key deliberately has no BindPFlag: instance_link.go owns
	// the "instance.deploy_key" viper binding, and a second binder here would
	// shadow it (last init wins). Run() reads this flag directly instead.
	startCmd.Flags().String("deploy-key", "", "deploy key for instance linking (OF_INSTANCE_DEPLOY_KEY); saved for future boots")

	startCmd.Flags().Bool("auto-link", false, "link this node to your account at boot with the stored deploy key (OF_INSTANCE_AUTO_LINK); each new peer ID spends a key use")
	_ = viper.BindPFlag("instance.auto_link", startCmd.Flags().Lookup("auto-link"))
}
