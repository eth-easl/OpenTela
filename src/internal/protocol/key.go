package protocol

import (
	"crypto/rand"
	mrand "math/rand"
	"opentela/internal/common"
	"os"
	"path/filepath"

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

// GenerateKeyFromSeed deterministically derives an Ed25519 libp2p identity
// from an integer seed. The same seed always yields the same key, so callers
// can assign stable, distinct PeerIDs (e.g. when several nodes share a $HOME
// or in reproducible mesh benchmarks). It does not touch the filesystem.
//
// Ed25519 is used rather than RSA on purpose: Go's RSA key generation is not
// reproducible from a seeded reader (it deliberately mixes in random bytes),
// so an RSA-based seed could never yield a stable PeerID.
func GenerateKeyFromSeed(seed int64) (crypto.PrivKey, error) {
	r := mrand.New(mrand.NewSource(seed))
	priv, _, err := crypto.GenerateEd25519Key(r)
	if err != nil {
		return nil, err
	}
	return priv, nil
}

// GenerateAndWriteKey generates a fresh RSA-2048 libp2p identity, persists it
// via WriteKeyToFile, and returns the in-memory key so callers don't need to
// re-load from disk. It is the same keygen flow used lazily by newHost when
// seed=0 and no key exists, exposed so that `otela init` can pre-generate
// the key without starting a libp2p host. A non-zero seed derives the key
// deterministically so the PeerID is reproducible across runs.
func GenerateAndWriteKey(seed int64) (crypto.PrivKey, error) {
	var priv crypto.PrivKey
	var err error
	if seed != 0 {
		priv, err = GenerateKeyFromSeed(seed)
	} else {
		priv, _, err = crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	}
	if err != nil {
		return nil, err
	}
	WriteKeyToFile(priv)
	return priv, nil
}
