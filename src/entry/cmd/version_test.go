package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestVersionCommandProperties(t *testing.T) {
	assert.Equal(t, "version", versionCmd.Use)
	assert.Equal(t, "Print the version of OpenTela", versionCmd.Short)
	assert.NotNil(t, versionCmd.Run)
}

func TestVersionCommandExecution(t *testing.T) {
	// Test that version command can execute without error
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionCmd.SetErr(&buf)

	versionCmd.Run(&cobra.Command{}, []string{})

	// The command should execute without panicking
	t.Log("Version command executed successfully")
}

func TestVersionCommandWithArguments(t *testing.T) {
	// Test that version command ignores arguments (as it should)
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionCmd.SetErr(&buf)

	versionCmd.Run(&cobra.Command{}, []string{"arg1", "arg2"})

	t.Log("Version command with arguments executed successfully")
}
