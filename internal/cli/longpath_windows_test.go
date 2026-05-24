//go:build windows

package cli_test

import (
	"strings"
	"testing"

	"github.com/forge/fdh/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestCheckLongPath_ShortPathOK(t *testing.T) {
	err := cli.CheckLongPath(`C:\short\path\skill`)
	assert.NoError(t, err)
}

func TestCheckLongPath_LongPathReportsRegistryState(t *testing.T) {
	// 270-char path forces the check to consult the registry.
	long := `C:\` + strings.Repeat("a", 267)
	err := cli.CheckLongPath(long)
	if err != nil {
		// If long paths are NOT enabled on this host, the error message
		// must cite the registry value (current state) so the user knows
		// what to fix.
		assert.Contains(t, err.Error(), "LongPathsEnabled")
		assert.Contains(t, err.Error(), "registry")
	}
	// If err is nil, long paths ARE enabled — also a valid outcome.
}
