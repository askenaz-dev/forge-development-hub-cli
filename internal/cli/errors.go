package cli

import (
	"errors"
	"fmt"
)

// Exit codes are part of the CLI's stable contract. Onboarding scripts and CI
// pipelines branch on these values. Do not renumber or remove an existing code
// without a corresponding spec change.
//
// See openspec/changes/installer-core/specs/skill-installer-cli/spec.md
// requirement "Stable, documented exit codes" for the authoritative list.
const (
	ExitOK              = 0 // success
	ExitGenericFailure  = 1 // any unexpected error
	ExitInvalidUsage    = 2 // bad command-line arguments
	ExitRegistryUnreach = 3 // configured registry could not be read
	ExitPortability     = 4 // portability lint failed
	ExitNoAgent         = 5 // no agents detected (or none compatible)
	ExitPermission      = 6 // filesystem permission denied
)

// cliError wraps a regular error with a CLI exit code.
type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string  { return e.err.Error() }
func (e *cliError) Unwrap() error  { return e.err }

// Errorf builds a cliError with the given exit code and message.
func Errorf(code int, format string, args ...any) error {
	return &cliError{code: code, err: fmt.Errorf(format, args...)}
}

// Wrap attaches an exit code to an existing error. Returns nil if err is nil.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &cliError{code: code, err: err}
}

// ExitCode extracts the exit code from an error. Returns ExitOK for nil and
// ExitGenericFailure for any error that does not carry an explicit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ExitGenericFailure
}
