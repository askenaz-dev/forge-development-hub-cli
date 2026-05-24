// Package main is the back-compat stub for the legacy `forge-installer`
// binary. Built alongside the primary `fdh` binary during the 90-day
// deprecation window declared in the dev-portal change's design.md and the
// fdh-cli-naming spec.
//
// Behavior:
//
//  1. Print a one-line deprecation notice to stderr naming `fdh` as the
//     replacement and `fdh config migrate` as the explicit migration step.
//  2. Look up `fdh` on PATH.
//  3. If found, exec it with the same arguments and inherit its exit code.
//  4. If not found, print a short install hint and exit with code 127.
//
// This file intentionally has no test coverage and no logic beyond what is
// written here — its job is to forward, not to implement features. When
// the 90-day window closes (see docs/release.md sunset date), delete the
// `cmd/forge-installer-stub/` directory and remove the artifact from
// release.yml.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const (
	exitNotInstalled = 127
)

func main() {
	fmt.Fprintln(os.Stderr,
		"DEPRECATED: 'forge-installer' has been renamed to 'fdh'. "+
			"This stub forwards to the new binary; install 'fdh' and update your scripts. "+
			"Run 'fdh config migrate' to move your config to the new location.")

	bin, err := exec.LookPath("fdh")
	if err != nil {
		fmt.Fprintln(os.Stderr,
			"\nError: 'fdh' is not on PATH.")
		fmt.Fprintln(os.Stderr,
			"Install it from the forge internal package manager and ensure it is on PATH.")
		fmt.Fprintln(os.Stderr,
			"See https://fdh.forge.internal/install for download links.")
		os.Exit(exitNotInstalled)
	}

	// Forward args + inherit stdin/stdout/stderr + propagate exit code.
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// If the child exited with a non-zero status, surface that code.
		ee := &exec.ExitError{}
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "failed to invoke fdh: %v\n", err)
		os.Exit(1)
	}
}
