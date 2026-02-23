package cmd

import (
	"os"
	"path/filepath"
	"testing"

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
			name: "invalid config file path",
			setup: func() {
				cfgFile = "/nonexistent/path/config.yaml"
			},
			cleanup: func() {
				cfgFile = ""
			},
			expectError: false, // Should use defaults
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
