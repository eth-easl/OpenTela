package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		expect  string
		wantErr bool
	}{
		{
			name:    "no arguments shows help",
			args:    []string{},
			wantErr: false,
		},
		{
			name:    "help flag",
			args:    []string{"--help"},
			wantErr: false,
		},
		{
			name:    "invalid command",
			args:    []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testRootCmd := rootcmd
			testRootCmd.SetArgs(tt.args)

			err := testRootCmd.Execute()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRootCommandProperties(t *testing.T) {
	assert.Equal(t, "otela", rootcmd.Use)
	assert.Equal(t, "OpenTela is a decentralized fabric for running machine learning applications.", rootcmd.Short)
	assert.Empty(t, rootcmd.Long)
	assert.NotNil(t, rootcmd.PersistentPreRunE)
	assert.NotNil(t, rootcmd.Run)
}

func TestInitConfig(t *testing.T) {
	tests := []struct {
		name        string
		setup       func()
		cleanup     func()
		expectError bool
	}{
		{
			name: "valid config file",
			setup: func() {
				tempDir := t.TempDir()
				cfgFile = filepath.Join(tempDir, "config.yaml")

				// Create a valid config file
				content := `
port: "8080"
name: "test-node"
tcpport: "43905"
udpport: "59820"
`
				err := os.WriteFile(cfgFile, []byte(content), 0644)
				require.NoError(t, err)
			},
			cleanup: func() {
				cfgFile = ""
			},
			expectError: false,
		},
		{
			name: "no config file uses defaults",
			setup: func() {
				cfgFile = ""
				// Set up a fake home directory
				home := t.TempDir()
				os.Setenv("HOME", home)
			},
			cleanup: func() {
				cfgFile = ""
				os.Unsetenv("HOME")
			},
			expectError: false,
		},
		{
			name: "missing config file under writable dir",
			setup: func() {
				// Point cfgFile at a location whose parent does not yet
				// exist but is creatable (under a temp dir). initConfig
				// should seed the config there instead of erroring.
				tempDir := t.TempDir()
				cfgFile = filepath.Join(tempDir, "nested", "cfg.yaml")
			},
			cleanup: func() {
				cfgFile = ""
			},
			expectError: false, // Should use defaults and seed the file.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			defer tt.cleanup()

			// Reset viper state
			viper.Reset()

			cmd := &cobra.Command{}
			err := initConfig(cmd)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInitConfigDefaults(t *testing.T) {
	// Reset viper state
	viper.Reset()

	// Create a temporary home directory
	tempHome := t.TempDir()
	os.Setenv("HOME", tempHome)
	defer os.Unsetenv("HOME")

	cfgFile = ""
	cmd := &cobra.Command{}
	err := initConfig(cmd)
	require.NoError(t, err)

	// Test that default values are set
	assert.Equal(t, "8092", viper.GetString("port"))
	assert.Equal(t, "relay", viper.GetString("name"))
	assert.Equal(t, "43905", viper.GetString("tcpport"))
	assert.Equal(t, "59820", viper.GetString("udpport"))
	assert.Equal(t, "24h", viper.GetString("crdt.tombstone_retention"))
	assert.Equal(t, "1h", viper.GetString("crdt.tombstone_compaction_interval"))
	assert.Equal(t, 512, viper.GetInt("crdt.tombstone_compaction_batch"))
	assert.Equal(t, "", viper.GetString("security.control_plane.url"))
	assert.Equal(t, "", viper.GetString("security.control_plane.token"))
	assert.Equal(t, 5*time.Second, viper.GetDuration("security.control_plane.timeout"))
	assert.Equal(t, 60*time.Second, viper.GetDuration("security.control_plane.cache_ttl"))
	assert.Equal(t, 2*time.Minute, viper.GetDuration("security.control_plane.stale_if_error"))
}

func TestInitConfigFlagBinding(t *testing.T) {
	// Reset viper state
	viper.Reset()

	tempHome := t.TempDir()
	os.Setenv("HOME", tempHome)
	defer os.Unsetenv("HOME")

	cfgFile = ""

	// Create a command with various flag types
	cmd := &cobra.Command{}
	cmd.Flags().Bool("test-bool", true, "test bool flag")
	cmd.Flags().String("test-string", "default", "test string flag")
	cmd.Flags().Int("test-int", 42, "test int flag")
	cmd.Flags().StringSlice("test-slice", []string{"a", "b"}, "test slice flag")

	// Simulate flag changes
	require.NoError(t, cmd.Flags().Set("test-bool", "false"))
	require.NoError(t, cmd.Flags().Set("test-string", "custom"))
	require.NoError(t, cmd.Flags().Set("test-int", "100"))
	require.NoError(t, cmd.Flags().Set("test-slice", "x,y,z"))

	err := initConfig(cmd)
	require.NoError(t, err)

	// Verify that flag values are bound to viper
	assert.Equal(t, false, viper.GetBool("test-bool"))
	assert.Equal(t, "custom", viper.GetString("test-string"))
	assert.Equal(t, 100, viper.GetInt("test-int"))
	assert.Equal(t, []string{"x", "y", "z"}, viper.GetStringSlice("test-slice"))
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		setup   func()
		wantErr bool
	}{
		{
			name:    "execute with no args",
			args:    []string{},
			wantErr: false, // Shows help, no error
		},
		{
			name:    "execute help",
			args:    []string{"--help"},
			wantErr: false,
		},
		{
			name: "execute with config file",
			args: []string{"--config", "/tmp/test.yaml"},
			setup: func() {
				// Create a dummy config file
				err := os.WriteFile("/tmp/test.yaml", []byte("port: 8080"), 0644)
				if err != nil {
					t.Skip("Cannot create test config file")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			// Create a test command instead of using the global Execute function
			// to avoid os.Exit complications
			testCmd := rootcmd
			testCmd.SetArgs(tt.args)

			err := testCmd.Execute()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInitFunction(t *testing.T) {
	// Test that the init function properly sets up flags
	// This is tested indirectly by checking that the root command has the expected flags

	flags := rootcmd.PersistentFlags()
	assert.NotNil(t, flags.Lookup("config"))

	// Check that subcommands are added
	assert.Contains(t, rootcmd.Commands(), startCmd)
	assert.Contains(t, rootcmd.Commands(), initCmd)
	assert.Contains(t, rootcmd.Commands(), versionCmd)
	assert.Contains(t, rootcmd.Commands(), updateCmd)
	assert.Contains(t, rootcmd.Commands(), balanceCmd)
	assert.Contains(t, rootcmd.Commands(), walletCmd)
}

func TestRootCommandHelpFunctionality(t *testing.T) {
	// Test that the root command's Run function properly calls Help()
	cmd := rootcmd
	cmd.SetArgs([]string{})

	// This should not panic and should show help
	err := cmd.Execute()
	assert.NoError(t, err)
}

func TestConfigFileVariable(t *testing.T) {
	// Test that cfgFile variable can be set and retrieved
	testFile := "/test/config.yaml"
	cfgFile = testFile
	assert.Equal(t, testFile, cfgFile)

	// Reset for other tests
	cfgFile = ""
}

func TestInitConfigSeedWriteHonorsConfigDir(t *testing.T) {
	// Regression test: when --config-dir is set (via viper key "config_dir")
	// and the target cfg.yaml does not yet exist, initConfig must seed the
	// file at <config_dir>/cfg.yaml — not at a relative path under the
	// current working directory.
	viper.Reset()
	defer viper.Reset()

	tempDir := t.TempDir()
	viper.Set("config_dir", tempDir)

	// Make sure the global cfgFile starts empty so the config_dir branch
	// at the top of initConfig populates it.
	cfgFile = ""
	defer func() { cfgFile = "" }()

	// Capture the working directory and assert nothing is written there.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	strayPath := filepath.Join(cwd, ".config", "opentela", "cfg.yaml")
	_, strayBefore := os.Stat(strayPath)

	cmd := &cobra.Command{}
	require.NoError(t, initConfig(cmd))

	expected := filepath.Join(tempDir, "cfg.yaml")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected seed config at %s, but it was not created: %v", expected, err)
	}

	// Confirm we did not also splatter a relative-path config under cwd.
	if strayBefore != nil && os.IsNotExist(strayBefore) {
		if _, err := os.Stat(strayPath); err == nil {
			t.Fatalf("seed config was written to stray relative path %s", strayPath)
		}
	}
}

func TestBillingConfigDefaults(t *testing.T) {
	// Reset viper state
	viper.Reset()

	// Create a temporary home directory
	tempHome := t.TempDir()
	os.Setenv("HOME", tempHome)
	defer os.Unsetenv("HOME")

	cfgFile = ""
	cmd := &cobra.Command{}
	err := initConfig(cmd)
	require.NoError(t, err)

	// The retired legacy settlement switch must keep defaulting to false (the
	// fail-fast guard in initConfig fires only when an operator enables it).
	assert.Equal(t, false, viper.GetBool("billing.enabled"), "billing.enabled should default to false")
}

// TestInitConfigRetiredBillingGuard verifies the fail-fast guard: enabling the
// retired legacy settlement switch (OF_BILLING_ENABLED / billing.enabled) must
// abort startup with a pointer to the API market instead of silently no-op'ing.
func TestInitConfigRetiredBillingGuard(t *testing.T) {
	viper.Reset()

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("OF_BILLING_ENABLED", "true")

	cfgFile = ""
	cmd := &cobra.Command{}
	err := initConfig(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "billing.enabled is set", "guard error should name the switch")
	assert.Contains(t, err.Error(), "API market", "guard error should point at the single billing rail")
}
