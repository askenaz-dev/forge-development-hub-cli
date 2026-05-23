// Package main is the entrypoint for the fdh CLI.
//
// The CLI installs spec-compliant Agent Skills (https://agentskills.io)
// to four AI coding agents — Claude Code, GitHub Copilot, OpenAI Codex,
// OpenCode — from a shared Git-backed skill registry.
//
// See the OpenSpec change `installer-core` in the falabella-development-hub
// repository for the requirements this CLI implements.
package main

import (
	"fmt"
	"os"

	"github.com/falabella/fdh/internal/cli"
)

// Build-time variables set via -ldflags during release.
// Example: -ldflags "-X main.version=1.0.0 -X main.commit=abc123 -X main.buildDate=2026-05-22"
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	if err := cli.Execute(cli.BuildInfo{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
	}); err != nil {
		// Errors from Execute already have the correct exit code attached.
		// Fall back to 1 for any error that didn't carry one.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
