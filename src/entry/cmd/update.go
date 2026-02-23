package cmd

import (
	"fmt"
	"net/http"
	"opentela/internal/common"
	"runtime"

	"github.com/minio/selfupdate"
	"github.com/spf13/cobra"
)

func doUpdate() error {
	// detect cpu arch
	arch := runtime.GOARCH
	url := "https://github.com/eth-easl/OpenTela/releases/latest/download/otela-" + arch
	common.Logger.Info("Downloading from ", url)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	err = selfupdate.Apply(resp.Body, selfupdate.Options{})
	if err != nil {
		return err
	}
	return nil
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the Open Compute Binary",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("current ocfcore version %s", common.JSONVersion.Version)
		fmt.Printf(" (commit: %s)", common.JSONVersion.Commit)
		fmt.Printf(" (built at: %s)", common.JSONVersion.Date)
		fmt.Println()
		err := doUpdate()
		if err != nil {
			common.Logger.Error("Error while updating: ", err)
		}
	},
}
