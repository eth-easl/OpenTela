package protocol

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// DeployKeyPath returns the on-disk location of the stored deploy key. It
// honors viper's "config_dir" override (set by the --config-dir flag) and
// falls back to ~/.config/opentela/keys/deploy_key — the same directory as
// the libp2p identity key, since both are node-local secrets.
func DeployKeyPath() (string, error) {
	if cd := viper.GetString("config_dir"); cd != "" {
		return filepath.Join(cd, "keys", "deploy_key"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opentela", "keys", "deploy_key"), nil
}

// StoreDeployKey persists the deploy key with 0600 permissions so later
// `otela start` runs can re-confirm the instance link automatically. The
// key authorizes only instance linking and can be revoked in the console.
func StoreDeployKey(key string) error {
	keyPath, err := DeployKeyPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

// LoadDeployKey returns the stored deploy key, or "" when none is stored
// (missing file, unreadable, or empty). Missing storage is a normal state
// for unlinked nodes, so it is not an error.
func LoadDeployKey() string {
	keyPath, err := DeployKeyPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
