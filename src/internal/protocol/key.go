package protocol

import (
	"crypto/rand"
	"io"
	"opentela/internal/common"
	"os"
	"path/filepath"
	"sync"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/viper"
)

// ResolveKeyPath returns the on-disk location of the libp2p identity key.
// It honors viper's "config_dir" override (set by the --config-dir flag) and
// falls back to ~/.config/opentela/keys/id.
func ResolveKeyPath() (string, error) {
	if cd := viper.GetString("config_dir"); cd != "" {
		return filepath.Join(cd, "keys", "id"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opentela", "keys", "id"), nil
}

// WriteKeyToFile marshals priv and writes it to the resolved key path,
// creating parent directories as needed. Exits the process on failure to
// preserve the historical behavior of newHost.
func WriteKeyToFile(priv crypto.PrivKey) {
	keyData, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		// Continuing would write an empty key file and silently poison the
		// node's persisted identity; fail hard like the branches below.
		common.Logger.Error("Error while marshalling private key: ", err)
		os.Exit(1)
	}
	keyPath, err := ResolveKeyPath()
	if err != nil {
		common.Logger.Error("Could not determine home directory: ", err)
		os.Exit(1)
	}
	err = os.MkdirAll(filepath.Dir(keyPath), os.ModePerm)
	if err != nil {
		common.Logger.Error("Could not create keys directory", "error", err)
		os.Exit(1)
	}
	err = os.WriteFile(keyPath, keyData, 0600)
	if err != nil {
		common.Logger.Error("Could not write key to file", err)
		os.Exit(1)
	}
}

// LoadKeyFromFile reads the libp2p identity key from disk and returns it.
// Returns nil if the file does not exist or cannot be parsed; errors are
// logged but not propagated, matching the historical behavior used by
// newHost when probing for an existing identity.
func LoadKeyFromFile() crypto.PrivKey {
	keyPath, err := ResolveKeyPath()
	if err != nil {
		return nil
	}
	common.Logger.Debug("Looking for keys under: ", keyPath)
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		common.Logger.Debug("No key file found: ", err)
		return nil
	}
	priv, err := crypto.UnmarshalPrivateKey(keyData)
	if err != nil {
		// A corrupt key file means this node will mint a fresh identity
		// (new peer ID) and overwrite the file — surface that, since peer
		// IDs appear in bootstrap lists and access-control config.
		common.Logger.Warn("Error while unmarshalling private key: ", err)
		return nil
	}
	return priv
}

// GenerateAndWriteKey generates a fresh RSA-2048 libp2p identity, persists it
// via WriteKeyToFile, and returns the in-memory key so callers don't need to
// re-load from disk. It is the same keygen flow used lazily by newHost when
// seed=0 and no key exists, exposed so that `otela init` can pre-generate
// the key without starting a libp2p host.
func GenerateAndWriteKey() (crypto.PrivKey, error) {
	priv, err := newKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	WriteKeyToFile(priv)
	return priv, nil
}

// newKey generates an RSA-2048 libp2p identity from r. Persisted, seeded and
// ephemeral identities all go through it so their key type cannot drift.
func newKey(r io.Reader) (crypto.PrivKey, error) {
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
	return priv, err
}

// EphemeralIdentityKey is the viper key (and `otela start` flag) that makes a
// node use EphemeralKey instead of the persisted key file. The hyphen keeps
// OF_EPHEMERAL-IDENTITY out of reach of AutomaticEnv, so the env form is
// bound explicitly as EphemeralIdentityEnv.
const EphemeralIdentityKey = "ephemeral-identity"

// EphemeralIdentityEnv is the environment variable that enables
// EphemeralIdentityKey, e.g. for SLURM scripts that configure nodes via OF_*.
const EphemeralIdentityEnv = "OF_EPHEMERAL_IDENTITY"

var (
	ephemeralKeyMu sync.Mutex
	ephemeralKey   crypto.PrivKey
)

// EphemeralKey returns this process's in-memory RSA-2048 libp2p identity,
// generating it on first use; nothing is read from or written to disk. Nodes
// that share one $HOME (e.g. the nodes of a multi-node Slurm job) would
// otherwise all load the same persisted key and announce the same peer ID.
// Every caller in the process (libp2p host, instance link) gets the same key.
func EphemeralKey() (crypto.PrivKey, error) {
	ephemeralKeyMu.Lock()
	defer ephemeralKeyMu.Unlock()
	if ephemeralKey == nil {
		priv, err := newKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		ephemeralKey = priv
	}
	return ephemeralKey, nil
}
