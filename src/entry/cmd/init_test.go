package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"opentela/internal/protocol"
)

func TestEnsureLibp2pKey_CreatesKeyWhenMissing(t *testing.T) {
	dir, err := os.MkdirTemp("", "otela-init-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(dir)

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config_dir", dir)

	err = ensureLibp2pKey(0)
	assert.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "keys", "id"))

	// Idempotent: second call must not fail and must not modify the file.
	info1, _ := os.Stat(filepath.Join(dir, "keys", "id"))
	err = ensureLibp2pKey(0)
	assert.NoError(t, err)
	info2, _ := os.Stat(filepath.Join(dir, "keys", "id"))
	assert.Equal(t, info1.ModTime(), info2.ModTime())

	// Loadable via the same code path otela start uses.
	priv := protocol.LoadKeyFromFile()
	assert.NotNil(t, priv)
}

func TestEnsureLibp2pKey_SeedIsDeterministic(t *testing.T) {
	dirA, err := os.MkdirTemp("", "otela-init-seed-a-*")
	assert.NoError(t, err)
	defer os.RemoveAll(dirA)
	dirB, err := os.MkdirTemp("", "otela-init-seed-b-*")
	assert.NoError(t, err)
	defer os.RemoveAll(dirB)

	// Same seed in different config roots must produce the same peer ID.
	viper.Reset()
	t.Cleanup(viper.Reset)

	viper.Set("config_dir", dirA)
	assert.NoError(t, ensureLibp2pKey(42))
	viper.Set("config_dir", dirB)
	assert.NoError(t, ensureLibp2pKey(42))

	keyA, err := os.ReadFile(filepath.Join(dirA, "keys", "id"))
	assert.NoError(t, err)
	keyB, err := os.ReadFile(filepath.Join(dirB, "keys", "id"))
	assert.NoError(t, err)
	assert.Equal(t, keyA, keyB, "same seed must yield the same persisted identity")

	// A different seed must yield a different identity.
	dirC, err := os.MkdirTemp("", "otela-init-seed-c-*")
	assert.NoError(t, err)
	defer os.RemoveAll(dirC)
	viper.Set("config_dir", dirC)
	assert.NoError(t, ensureLibp2pKey(43))
	keyC, err := os.ReadFile(filepath.Join(dirC, "keys", "id"))
	assert.NoError(t, err)
	assert.NotEqual(t, keyA, keyC, "different seeds must yield different identities")
}
