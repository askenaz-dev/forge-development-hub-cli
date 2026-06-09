package gitops

import (
	"context"
	"fmt"
	"strings"
)

// Requestor is the server-verified portal user on whose behalf the bot composes
// a PR. It is TRUSTED metadata: the handler fills it from the principal /
// service-credential's mapped role and the BFF-forwarded identity, NEVER from
// client free-text used for authorization (design D2; PR attribution only).
type Requestor struct {
	// Name / Email identify the portal user for PR credit (may be empty).
	Name  string
	Email string
	// Role is the server-verified portal role (author/publisher/admin).
	Role string
}

// label returns a human credit string for the PR body.
func (r Requestor) label() string {
	who := strings.TrimSpace(r.Name)
	if who == "" {
		who = strings.TrimSpace(r.Email)
	}
	if who == "" {
		who = "a portal user"
	}
	role := strings.TrimSpace(r.Role)
	if role == "" {
		role = "unknown-role"
	}
	return fmt.Sprintf("%s (`%s`)", who, role)
}

// Result is the outcome of a composer. URL is the pull request URL — either the
// freshly-opened PR or, when AlreadyOpen is true, the pre-existing open PR for
// the deterministic branch (idempotency, design D7). Branch is the deterministic
// head branch the action targeted.
type Result struct {
	URL         string
	Branch      string
	AlreadyOpen bool
}

// proposeFooter is appended to every PR body. It states the propose-only
// guarantee verbatim so reviewers and the credited user share the same framing.
const proposeFooter = "Proposed via the portal — not part of the catalog until reviewed and merged."

// buildPRBody composes the PR description: it credits the requesting user+role
// (trusted metadata), describes the web action, and states the propose-only
// note. action is a short human description (e.g. "Import skill `card-grid`").
func buildPRBody(action string, r Requestor, details ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", action)
	fmt.Fprintf(&b, "Requested via the portal by %s.\n", r.label())
	if len(details) > 0 {
		b.WriteString("\n")
		for _, d := range details {
			if strings.TrimSpace(d) == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", proposeFooter)
	return b.String()
}

// idempotentExisting checks for an existing open PR on branch and, if present,
// returns a Result carrying its URL (AlreadyOpen=true). The bool reports whether
// such a PR was found (so the caller short-circuits before composing). Every
// composer calls this FIRST (design D7).
func idempotentExisting(ctx context.Context, c Client, branch string) (Result, bool, error) {
	url, found, err := c.FindOpenPR(ctx, branch)
	if err != nil {
		return Result{}, false, err
	}
	if found {
		return Result{URL: url, Branch: branch, AlreadyOpen: true}, true, nil
	}
	return Result{}, false, nil
}

// prepareBranch creates the deterministic branch at the latest default-branch
// tip and returns the base SHA to commit against. It always branches from the
// latest tip (design D5/D7) and NEVER force-resets an existing branch.
//
// Every composer calls idempotentExisting (FindOpenPR) BEFORE this, so reaching
// here with the branch already present means a stale/orphaned branch with no
// open PR (a prior run that committed but failed to open the PR, or a merged-
// but-undeleted branch). Committing a latest-tip-parented commit onto such a
// branch would non-fast-forward and fail; rather than loop on an opaque 500,
// return ErrBranchConflict so the handler surfaces a typed, recoverable 409.
func prepareBranch(ctx context.Context, c Client, branch string) (baseSHA string, err error) {
	baseSHA, err = c.DefaultBranchSHA(ctx)
	if err != nil {
		return "", err
	}
	exists, err := c.BranchExists(ctx, branch)
	if err != nil {
		return "", err
	}
	if exists {
		return "", ErrBranchConflict
	}
	if err := c.CreateBranch(ctx, branch, baseSHA); err != nil {
		return "", err
	}
	return baseSHA, nil
}

// hubRegistryPath / hubHarnessesPath are the canonical repo paths the composers
// read and edit.
const (
	hubRegistryPath  = "hub/registry.yaml"
	hubHarnessesPath = "hub/harnesses.yaml"
)
