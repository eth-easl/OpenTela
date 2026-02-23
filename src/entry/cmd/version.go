package cmd

import (
	"fmt"
	"opentela/internal/common"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of OpenTela",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("OpenTela version %s", common.JSONVersion.Version)
		fmt.Printf(" (commit: %s)", common.JSONVersion.Commit)
		fmt.Printf(" (built at: %s)", common.JSONVersion.Date)
		fmt.Println()
	},
}
