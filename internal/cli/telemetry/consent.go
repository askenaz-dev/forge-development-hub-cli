package telemetry

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// consentPrompt is the one-time first-run consent text. It states what is and
// is not collected, defaults to declining, and links the privacy policy.
// English-only per project convention.
const consentPrompt = `fdh can send anonymous, opt-in usage telemetry to help improve the platform.

What would be sent (only if you opt in):
  - the event type (install/download/resolve), and the component's
    kind/namespace/name/version/content-hash, scope, and registry
  - a coarse OS bucket (darwin/linux/windows) and locale (es/en)
  - a pseudonymous, rotating install id (a salted random hash)

What is NEVER collected:
  - no username, email, hostname, IP address, file paths, or file contents

Telemetry is OFF by default and fully reversible (fdh telemetry disable).
The pseudonymous id can be rotated at any time (fdh telemetry rotate).
Privacy policy: ` + PrivacyPolicyURL + `

Enable anonymous telemetry? [y/N]: `

// MaybePrompt shows the one-time consent prompt and persists the answer,
// returning the granted decision. It returns (granted=false, asked=false)
// without prompting when:
//   - the prompt was already answered once (one-time), or
//   - the session is non-interactive (no TTY / CI) — absence of an explicit
//     opt-in is treated as declined and NO prompt is shown, or
//   - an env/config signal already decided enablement (caller passes
//     alreadyDecided=true so we never prompt over an explicit choice).
//
// interactive reflects whether stdin is a TTY (the caller computes it; this
// package does not import the term helper). in/out are the command's IO so
// tests can drive the prompt deterministically.
func (m *Manager) MaybePrompt(interactive, alreadyDecided bool, in io.Reader, out io.Writer) (granted, asked bool) {
	if alreadyDecided {
		return false, false
	}
	if !interactive {
		// No TTY / CI: never prompt; stay off (declined by omission).
		return false, false
	}
	m.load()
	if m.st.ConsentAsked {
		return m.st.ConsentGranted, false
	}

	fmt.Fprint(out, consentPrompt)
	answer := readLine(in)
	granted = isAffirmative(answer)
	// Persist so the prompt is one-time, regardless of the answer. A save
	// failure is non-fatal: worst case the prompt re-appears next run.
	_ = m.RecordConsent(granted)
	return granted, true
}

// readLine reads a single line of input, returning "" on EOF/error so the
// default (decline) applies.
func readLine(in io.Reader) string {
	if in == nil {
		return ""
	}
	r := bufio.NewReader(in)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

// isAffirmative reports whether the user explicitly opted in. The default
// (empty / anything else) is decline.
func isAffirmative(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}
