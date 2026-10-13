package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"opentela/internal/protocol"
)

func TestPeerIDCommand_PrintsPeerID(t *testing.T) {
	dir, err := os.MkdirTemp("", "otela-peerid-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(dir)

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config_dir", dir)
	_, err = protocol.GenerateAndWriteKey(0)
	assert.NoError(t, err)

	var out bytes.Buffer
	peerIDCmd.SetOut(&out)
	err = peerIDCmd.RunE(peerIDCmd, nil)
	assert.NoError(t, err)
	printed := out.String()
	// libp2p PeerIDs (RSA-derived) are base58 with no whitespace, ~46+ chars.
	assert.GreaterOrEqual(t, len(printed), 40)
	assert.NotContains(t, printed, " ")
}
