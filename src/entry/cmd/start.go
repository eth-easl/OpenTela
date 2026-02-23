package cmd

import (
	"opentela/internal/common"
	"opentela/internal/protocol"
	"opentela/internal/server"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start listening for incoming connections",
	Run: func(cmd *cobra.Command, args []string) {
		// check if cleanslate is set
		if viper.GetBool("cleanslate") {
			// clean slate, by removing the database
			common.Logger.Info("Cleaning slate")
			protocol.ClearCRDTStore()
		}
		server.StartServer()
	}}
