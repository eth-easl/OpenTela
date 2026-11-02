package common

import (
	"os"
	"path"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

// DataDir returns the configured node data directory (--datadir / OF_DATADIR)
// with a leading ~ expanded, or "" when unset.
func DataDir() string {
	dataDir := viper.GetString("datadir")
	expanded, err := homedir.Expand(dataDir)
	if err != nil {
		Logger.Warn("Could not expand ~ in datadir, using it verbatim", "datadir", dataDir, "error", err)
		return dataDir
	}
	return expanded
}

// GetHomePath returns the directory holding the peer database: <datadir>/ocfcore
// when datadir is set (--datadir / OF_DATADIR), otherwise ~/.ocfcore.
func GetHomePath() string {
	var ocfcorePath string
	if dataDir := DataDir(); dataDir != "" {
		ocfcorePath = path.Join(dataDir, "ocfcore")
	} else {
		home, err := homedir.Dir()
		if err != nil {
			Logger.Error("Could not get home directory", "error", err)
			home = "."
		}
		ocfcorePath = path.Join(home, ".ocfcore")
	}
	if _, err := os.Stat(ocfcorePath); os.IsNotExist(err) {
		err := os.MkdirAll(ocfcorePath, 0755)
		if err != nil {
			Logger.Error("Could not create ocfcore directory", "error", err)
			return "."
		}
	}
	return ocfcorePath
}

func GetDBPath(nodeid string) string {
	return path.Join(GetHomePath(), "ocfcore."+nodeid+".db")
}
