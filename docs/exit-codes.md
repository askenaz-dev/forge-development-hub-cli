# Exit codes

`fdh` and the install scripts use a stable, documented set of exit codes.
Onboarding scripts and CI pipelines branch on these values — they will
not be renumbered or repurposed without a corresponding spec change.

## CLI (`fdh`)

| Code | Constant in `internal/cli/errors.go` | Meaning |
|---:|---|---|
| 0 | `ExitOK` | Success. |
| 1 | `ExitGenericFailure` | Any unexpected error (panic recovery, internal bug). |
| 2 | `ExitInvalidUsage` | Bad command-line arguments (unknown flag, conflicting options, malformed value). |
| 3 | `ExitRegistryUnreach` | The configured registry could not be read. |
| 4 | `ExitPortability` | Portability lint failed during `fdh install`. |
| 5 | `ExitNoAgent` | No agents detected (or none compatible with the requested skill). |
| 6 | `ExitPermission` | Filesystem permission denied (e.g. can't write the config or a skill directory). |
| 7 | `ExitValidation` | `fdh validate-registry` rejected the input or `fdh init --skills <name>` referenced an unknown skill. |

### Why exit 3 is *not* "permission denied"

A previous draft of the implementation contract listed `3 = permission`,
`4 = registry`, `5 = validation`, `127 = binary not found`. That listing
conflicted with the values already in `errors.go` and the pilot users'
scripts. We preserved the existing semantics (registry vs. portability
vs. no-agent vs. permission) and added `ExitValidation = 7` for the new
subcommand. The spec wording should be updated to match — see the
"Deviations" note in the change's session 1 summary.

## Stub binary (`forge-installer`)

The legacy stub uses a single non-zero code in addition to whatever it
forwards from `fdh`:

| Code | Meaning |
|---:|---|
| 0 | Forwarded successfully (and `fdh` itself returned 0). |
| 127 | `fdh` is not on PATH; install it and re-run. |
| ... | Any other non-zero value is the exit code `fdh` returned, propagated verbatim. |

## Install scripts (`install.sh`, `install.ps1`)

Both scripts share the same set:

| Code | Meaning |
|---:|---|
| 0 | Install succeeded (or already up-to-date — idempotent). |
| 1 | Generic failure (extracted binary missing, install dir not writable, etc.). |
| 2 | Invalid usage (unknown flag, missing argument). |
| 3 | Unsupported OS/arch (`uname -s` not in `{Darwin, Linux}` or `uname -m` not in `{x86_64, arm64}`; Windows arm64). |
| 4 | Network error fetching the manifest or a binary. |
| 5 | SHA-256 checksum mismatch — installation aborted. |

## How to branch on exit codes from CI

```sh
# bash
fdh validate-registry "$REPO"
case $? in
  0) echo "registry ok" ;;
  2) echo "bad invocation"; exit 2 ;;
  7) echo "registry invalid; see output above"; exit 1 ;;
  *) echo "unexpected fdh failure"; exit 1 ;;
esac
```

```powershell
# PowerShell
fdh validate-registry $REPO
switch ($LASTEXITCODE) {
  0 { Write-Host "registry ok" }
  2 { Write-Host "bad invocation"; exit 2 }
  7 { Write-Host "registry invalid; see output above"; exit 1 }
  default { Write-Host "unexpected fdh failure"; exit 1 }
}
```

## Adding a new exit code

1. Pick the next free integer ≥ 8.
2. Declare a constant in `internal/cli/errors.go` with a one-line comment
   explaining the condition.
3. Add a row to the table above.
4. Update any spec that promises a closed set of codes.
5. Never renumber an existing code, even if the value seems wrong in
   hindsight. Add a new one and deprecate the old one in docs.
