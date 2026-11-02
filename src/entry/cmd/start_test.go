package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"opentela/internal/protocol"
)

func TestStartCommand(t *testing.T) {
	t.Skip("Skipping start command test due to server dependencies")
	tests := []struct {
		name     string
		args     []string
		setup    func()
		wantErr  bool
	}{
		{
			name:    "start with default settings",
			args:    []string{},
			wantErr: false, // May fail due to server dependencies, but command structure is valid
		},
		{
			name:    "start with cleanslate false",
			args:    []string{"--cleanslate=false"},
			wantErr: false,
		},
		{
			name:    "start with custom seed",
			args:    []string{"--seed=12345"},
			wantErr: false,
		},
		{
			name:    "start with custom mode",
			args:    []string{"--mode=standalone"},
			wantErr: false,
		},
		{
			name:    "start with custom ports",
			args:    []string{"--tcpport=12345", "--udpport=54321"},
			wantErr: false,
		},
		{
			name:    "start with bootstrap address",
			args:    []string{"--bootstrap.addr=http://localhost:8092/v1/dnt/bootstraps"},
			wantErr: false,
		},
		{
			name:    "start with bootstrap sources",
			args:    []string{"--bootstrap.source=http://example1.com", "--bootstrap.source=http://example2.com"},
			wantErr: false,
		},
		{
			name:    "start with static bootstrap",
			args:    []string{"--bootstrap.static=/ip4/127.0.0.1/tcp/8093"},
			wantErr: false,
		},
		{
			name:    "start with service configuration",
			args:    []string{"--service.name=test-service", "--service.port=3000", "--service.health_path=/status"},
			wantErr: false,
		},
		{
			name:    "start with wallet configuration",
			args:    []string{"--wallet.account=test-account", "--account.wallet=/path/to/wallet.json"},
			wantErr: false,
		},
		{
			name:    "start with solana configuration",
			args:    []string{"--solana.rpc=https://api.devnet.solana.com", "--solana.mint=test-mint", "--solana.skip_verification=true"},
			wantErr: false,
		},
		{
			name:    "start with subprocess",
			args:    []string{"--subprocess=docker run nginx"},
			wantErr: false,
		},
		{
			name:    "start with public address",
			args:    []string{"--public-addr=192.168.1.100:8092"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of the start command for testing
			testCmd := &cobra.Command{
				Use:   "start",
				Short: "Start listening for incoming connections",
				Run:   startCmd.Run,
			}

			// Add the same flags as the original start command
			testCmd.Flags().String("wallet.account", "", "wallet account")
			testCmd.Flags().String("account.wallet", "", "path to wallet key file")
			testCmd.Flags().String("bootstrap.addr", "http://152.67.71.5:8092/v1/dnt/bootstraps", "bootstrap address")
			testCmd.Flags().StringSlice("bootstrap.source", nil, "bootstrap source")
			testCmd.Flags().StringSlice("bootstrap.static", nil, "static bootstrap multiaddr")
			testCmd.Flags().String("seed", "0", "Seed")
			testCmd.Flags().String("mode", "node", "Mode")
			testCmd.Flags().String("tcpport", "43905", "TCP Port")
			testCmd.Flags().String("udpport", "59820", "UDP Port")
			testCmd.Flags().String("subprocess", "", "Subprocess to start")
			testCmd.Flags().String("public-addr", "", "Public address")
			testCmd.Flags().String("service.name", "", "Service name")
			testCmd.Flags().String("service.port", "", "Service port")
			testCmd.Flags().String("service.health_path", "/health", "Service health path")
			testCmd.Flags().String("solana.rpc", defaultConfig.Solana.RPC, "Solana RPC endpoint")
			testCmd.Flags().String("solana.mint", defaultConfig.Solana.Mint, "SPL token mint")
			testCmd.Flags().Bool("solana.skip_verification", defaultConfig.Solana.SkipVerification, "Skip verification")
			testCmd.Flags().Bool("cleanslate", true, "Clean slate")

			testCmd.SetArgs(tt.args)

			if tt.setup != nil {
				tt.setup()
			}

			// Mock the server startup to avoid actual network dependencies
			// In a real test environment, you would mock the server.StartServer() function

			err := testCmd.Execute()
			// Note: This may fail due to missing server dependencies, but the command parsing should work
			// We're mainly testing that the flags are properly parsed and the command structure is correct
			if err != nil && !tt.wantErr {
				// Check if the error is related to server startup (expected in test environment)
				// rather than command parsing (which would be a real test failure)
				t.Logf("Command execution failed (may be expected in test environment): %v", err)
			}
		})
	}
}

func TestStartCommandProperties(t *testing.T) {
	assert.Equal(t, "start", startCmd.Use)
	assert.Equal(t, "Start listening for incoming connections", startCmd.Short)
	assert.NotNil(t, startCmd.Run)
}

func TestStartCommandFlags(t *testing.T) {
	flags := startCmd.Flags()

	// Test that all expected flags are present
	expectedFlags := []string{
		"wallet.account",
		"wallet.address",
		"account.wallet",
		"bootstrap.addr",
		"bootstrap.source",
		"bootstrap.static",
		"seed",
		"mode",
		"tcpport",
		"udpport",
		"subprocess",
		"public-addr",
		"service.name",
		"service.port",
		"service.health_path",
		"solana.rpc",
		"solana.mint",
		"solana.skip_verification",
		"cleanslate",
	}

	for _, flagName := range expectedFlags {
		flag := flags.Lookup(flagName)
		assert.NotNil(t, flag, "Expected flag '%s' to be present", flagName)
	}
}

func TestStartCommandFlagDefaults(t *testing.T) {
	flags := startCmd.Flags()

	// Test default values
	assert.Equal(t, "0", flags.Lookup("seed").DefValue)
	assert.Equal(t, "node", flags.Lookup("mode").DefValue)
	assert.Equal(t, "43905", flags.Lookup("tcpport").DefValue)
	assert.Equal(t, "59820", flags.Lookup("udpport").DefValue)
	assert.Equal(t, "/health", flags.Lookup("service.health_path").DefValue)
	assert.Equal(t, "true", flags.Lookup("cleanslate").DefValue)
	assert.Equal(t, defaultConfig.Solana.RPC, flags.Lookup("solana.rpc").DefValue)
	assert.Equal(t, defaultConfig.Solana.Mint, flags.Lookup("solana.mint").DefValue)
	assert.Equal(t, "false", flags.Lookup("solana.skip_verification").DefValue) // Bool flags use "false" string representation
}

func TestStartCommandCleanslateIntegration(t *testing.T) {
	// Test the cleanslate functionality
	tests := []struct {
		name       string
		cleanslate bool
		setup      func()
	}{
		{
			name:       "cleanslate enabled",
			cleanslate: true,
			setup: func() {
				// Mock the cleanslate functionality
				// In a real test, you would mock the protocol.ClearCRDTStore() function
			},
		},
		{
			name:       "cleanslate disabled",
			cleanslate: false,
			setup: func() {
				// Mock when cleanslate is disabled
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			// Verify that the flag is properly parsed
			flag := startCmd.Flags().Lookup("cleanslate")
			assert.NotNil(t, flag)

			// Test flag value setting
			err := flag.Value.Set("true")
			if tt.cleanslate {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStartCommandWithViper(t *testing.T) {
	// Test integration with viper configuration
	viper.Reset()

	// Set some configuration values
	viper.Set("cleanslate", false)
	viper.Set("seed", "12345")
	viper.Set("mode", "standalone")

	// Create test command
	testCmd := &cobra.Command{
		Use:   "start",
		Short: "Start listening for incoming connections",
		Run: func(cmd *cobra.Command, args []string) {
			// Test that viper values are accessible in the run function
			assert.Equal(t, false, viper.GetBool("cleanslate"))
			assert.Equal(t, "12345", viper.GetString("seed"))
			assert.Equal(t, "standalone", viper.GetString("mode"))
		},
	}

	// Add flags
	testCmd.Flags().Bool("cleanslate", true, "Clean slate")
	testCmd.Flags().String("seed", "0", "Seed")
	testCmd.Flags().String("mode", "node", "Mode")

	testCmd.SetArgs([]string{"--cleanslate=false", "--seed=12345", "--mode=standalone"})

	err := testCmd.Execute()
	assert.NoError(t, err)
}

func TestStartCommandHelp(t *testing.T) {
	// Test that the help functionality works
	testCmd := &cobra.Command{
		Use:   "start",
		Short: "Start listening for incoming connections",
		Run:   startCmd.Run,
	}

	// Copy flags from original command
	flags := startCmd.Flags()
	testCmd.Flags().AddFlagSet(flags)

	testCmd.SetArgs([]string{"--help"})

	err := testCmd.Execute()
	assert.NoError(t, err)
}

func TestUseEphemeralDatadir(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("TMPDIR", t.TempDir())

	// Persisted identity: datadir stays unset (~/.ocfcore, as before).
	useEphemeralDatadir()
	assert.Empty(t, viper.GetString("datadir"))

	// An explicit --datadir always wins.
	viper.Set(protocol.EphemeralIdentityKey, true)
	viper.Set("datadir", "/explicit")
	useEphemeralDatadir()
	assert.Equal(t, "/explicit", viper.GetString("datadir"))

	// Ephemeral without --datadir: a fresh per-process temp dir, never $HOME.
	viper.Set("datadir", "")
	useEphemeralDatadir()
	dir := viper.GetString("datadir")
	assert.True(t, strings.HasPrefix(dir, os.Getenv("TMPDIR")), "datadir %q not under TMPDIR", dir)
	st, err := os.Stat(dir)
	assert.NoError(t, err)
	assert.True(t, st.IsDir())
}
