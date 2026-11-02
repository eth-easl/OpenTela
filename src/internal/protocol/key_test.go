package protocol

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveKeyPath_DefaultsToHome(t *testing.T) {
	viper.Reset()
	home, _ := os.UserHomeDir()
	got, err := ResolveKeyPath()
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "opentela", "keys", "id"), got)
}

func TestResolveKeyPath_HonorsConfigDir(t *testing.T) {
	viper.Reset()
	viper.Set("config_dir", "/tmp/otela-bench-xyz")
	got, err := ResolveKeyPath()
	assert.NoError(t, err)
	assert.Equal(t, "/tmp/otela-bench-xyz/keys/id", got)
}

func TestKeyRoundTrip(t *testing.T) {
	// Generate a libp2p RSA private key
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	require.NoError(t, err, "key generation should succeed")

	// Marshal the private key to bytes
	keyData, err := crypto.MarshalPrivateKey(priv)
	require.NoError(t, err, "marshalling private key should succeed")

	// Unmarshal the bytes back to a private key
	restored, err := crypto.UnmarshalPrivateKey(keyData)
	require.NoError(t, err, "unmarshalling private key should succeed")

	// Verify the restored key matches the original by comparing their raw bytes
	origBytes, err := crypto.MarshalPrivateKey(priv)
	require.NoError(t, err)
	restoredBytes, err := crypto.MarshalPrivateKey(restored)
	require.NoError(t, err)

	assert.Equal(t, origBytes, restoredBytes, "round-tripped key bytes should match original")

	// Also verify the public keys match
	assert.True(t, priv.GetPublic().Equals(restored.GetPublic()), "public keys should be equal")
}

// useEphemeralIdentity turns on --ephemeral-identity with a fresh config dir
// and forgets the process's ephemeral key, as if this were a new process.
func useEphemeralIdentity(t *testing.T) string {
	t.Helper()
	viper.Reset()
	dir := t.TempDir()
	viper.Set("config_dir", dir)
	viper.Set(EphemeralIdentityKey, true)
	forgetEphemeralKey()
	t.Cleanup(func() {
		viper.Reset()
		forgetEphemeralKey()
	})
	return dir
}

func forgetEphemeralKey() {
	ephemeralKeyMu.Lock()
	defer ephemeralKeyMu.Unlock()
	ephemeralKey = nil
}

func TestLoadOrCreateIdentity_EphemeralIgnoresKeyFile(t *testing.T) {
	// Nodes sharing one $HOME (multi-node Slurm jobs) must not end up with the
	// persisted key, and must not overwrite it either.
	dir := useEphemeralIdentity(t)
	viper.Set(EphemeralIdentityKey, false)
	persisted, err := GenerateAndWriteKey()
	require.NoError(t, err)
	keyPath := filepath.Join(dir, "keys", "id")
	before, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	viper.Set(EphemeralIdentityKey, true)
	for _, seed := range []int64{0, 1} {
		priv, err := NodeIdentity(seed)
		require.NoError(t, err)
		assert.False(t, priv.Equals(persisted), "seed=%d: ephemeral identity must not reuse the key file", seed)
	}
	after, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "ephemeral identity must not overwrite the key file")
}

func TestLoadOrCreateIdentity_EphemeralWritesNoKeyFile(t *testing.T) {
	dir := useEphemeralIdentity(t)

	_, err := NodeIdentity(0)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "keys", "id"))
	assert.True(t, os.IsNotExist(err), "no key file should be written")
}

func TestEphemeralKey_OnePerProcess(t *testing.T) {
	// Within one process the host and the instance link must present the same
	// identity; a new process (simulated by forgetting the key) gets a new one,
	// even with the same seed and the same config dir.
	useEphemeralIdentity(t)
	first, err := NodeIdentity(1)
	require.NoError(t, err)
	again, err := EphemeralKey()
	require.NoError(t, err)
	assert.True(t, first.Equals(again), "same process: same identity")

	forgetEphemeralKey()
	next, err := NodeIdentity(1)
	require.NoError(t, err)
	assert.False(t, first.Equals(next), "new process: new identity")
}

func TestEphemeralKey_ConcurrentCallersShareOneKey(t *testing.T) {
	useEphemeralIdentity(t)
	const n = 8
	keys := make([]crypto.PrivKey, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, err := EphemeralKey()
			assert.NoError(t, err)
			keys[i] = k
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		assert.True(t, keys[0].Equals(keys[i]), "caller %d got a different key", i)
	}
}

func TestLoadOrCreateIdentity_DefaultReusesKeyFile(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config_dir", t.TempDir())

	first, err := NodeIdentity(0)
	require.NoError(t, err)
	second, err := NodeIdentity(0)
	require.NoError(t, err)
	assert.True(t, first.Equals(second), "without --ephemeral-identity the identity stays stable")
}
