package main

import (
	"opentela/entry/cmd"
	"opentela/internal/common"
)

var (
	// Populated during build
	version    = "dev"
	commitHash = "?"
	buildDate  = ""
	// buildSecret left for future use to verify official builds
	buildSecret string
)

func main() {
	common.JSONVersion.Version = version
	common.JSONVersion.Commit = commitHash
	common.JSONVersion.Date = buildDate
	_ = buildSecret
	cmd.Execute()
}
