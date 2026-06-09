// Package gitops is the portal's server-side write surface to the hub repo.
//
// It is the API-resident counterpart of the CLI's `fdh share` (see
// internal/cli/authoring.go): it composes a single pull request per web write
// action on a deterministic branch and opens it on forge-development-hub via a
// portal-owned GitHub App ("the bot"). The security spine is non-negotiable:
//
//   - PROPOSE-ONLY. The Client interface exposes NO merge primitive and the
//     real implementation calls no merge endpoint. The bot can open a PR but
//     can never land it; a human merges under branch protection (design D3).
//   - CODE IS THE SOURCE OF TRUTH. Nothing here writes the hub working tree as
//     a server mutation. The pull request is the only artifact (design: "no
//     draft store, no shadow catalog").
//   - TWO CREDENTIALS, NEVER CONFLATED. This package holds ONLY the App
//     installation token (bot→GitHub). The portal→API service credential is a
//     separate concern handled by the auth middleware (design D2).
//   - NON-FATAL CONSTRUCTION. When the GitHub App env is absent, New returns a
//     DISABLED client whose every method returns ErrGitopsNotConfigured, so the
//     API still boots and serves catalog/admin reads (portal-runtime-resilience).
//
// The composers (compose_*.go) operate ONLY against the Client interface so
// unit tests drive them with an in-memory fake (no network, no App key).
package gitops

import (
	"context"
	"errors"
)

// ErrGitopsNotConfigured is the typed sentinel returned by every method of a
// DISABLED client (the GitHub App env was absent at construction). Handlers map
// it to a 503 gitops_not_configured — NEVER a 500 or a crash. The code ships
// "dark" and lights up when the App secret is wired by an org owner.
var ErrGitopsNotConfigured = errors.New("gitops: GitHub App is not configured")

// ErrNameCollision signals an import whose destination directory or registry
// entry already exists in the hub (mirrors the CLI copyTree abort-on-existing).
// Handlers map it to 422.
var ErrNameCollision = errors.New("gitops: component already exists in the hub")

// ErrBranchConflict signals that the deterministic branch for an action already
// exists with NO open pull request — a stale/orphaned branch from a prior
// partial run (committed but the PR open failed) or a merged-but-undeleted
// branch. Committing a latest-tip-parented commit onto such a branch would be a
// non-fast-forward, so the composer refuses (it never force-pushes, design D7)
// and returns this; handlers map it to a typed, recoverable 409 instead of a
// permanent opaque 500. Recovery is deleting the stale branch on the hub repo.
var ErrBranchConflict = errors.New("gitops: a branch for this action already exists without an open pull request; delete the stale branch on the hub repo and retry")

// ErrValidation signals that the server-side validation gate (bundle/scan/
// portability) failed BEFORE any push. Its message names the failing check.
// Handlers map it to 422.
type ErrValidation struct {
	// Check is the gate that failed: "bundle", "validate", "portability", or "scan".
	Check string
	// Detail is the underlying, human-readable failure.
	Detail string
}

func (e *ErrValidation) Error() string {
	return "gitops: validation failed at " + e.Check + ": " + e.Detail
}

// ErrLifecycle signals a rejected curate transition (e.g. un-yank — lifecycle
// is forward-only active→deprecated→yanked). Handlers map it to 422.
type ErrLifecycle struct {
	Detail string
}

func (e *ErrLifecycle) Error() string { return "gitops: " + e.Detail }

// FileChange is one repo-relative file mutation in an atomic commit. A change
// either sets Content (add/update) or sets Delete=true (removal). Path is always
// forward-slash, repo-root-relative (e.g. "skills/card-grid/SKILL.md").
type FileChange struct {
	Path    string
	Content []byte
	Delete  bool
}

// Client is the minimal GitHub primitive set the composers need. It is a small,
// mockable surface so tests use an in-memory fake. There is DELIBERATELY no
// Merge method: the bot is propose-only (design D3). A reviewer-style test
// (no_merge_test.go) asserts this interface exposes only read/branch/commit/PR
// primitives and nothing that could land a change.
type Client interface {
	// Enabled reports whether the client can talk to GitHub. A disabled client
	// (missing App env) returns false and every other method returns
	// ErrGitopsNotConfigured.
	Enabled() bool

	// DefaultBranch returns the repo's default branch name (e.g. "main").
	DefaultBranch(ctx context.Context) (string, error)

	// DefaultBranchSHA returns the commit SHA at the tip of the default branch.
	// Composers always branch from this latest tip (design D5/D7).
	DefaultBranchSHA(ctx context.Context) (sha string, err error)

	// BranchExists reports whether a branch ref exists in the repo.
	BranchExists(ctx context.Context, name string) (bool, error)

	// CreateBranch creates a new branch `name` pointing at fromSHA.
	CreateBranch(ctx context.Context, name, fromSHA string) error

	// GetFile fetches the decoded content of a repo file at ref (branch, tag, or
	// SHA). found is false (nil error) when the path does not exist at ref.
	GetFile(ctx context.Context, path, ref string) (content []byte, found bool, err error)

	// CommitFiles performs an ATOMIC multi-file commit on `branch`: it creates a
	// tree based on baseSHA's tree with every FileChange applied (blobs created
	// for adds/updates, entries dropped for deletes), creates a commit with
	// baseSHA as parent, and fast-forwards the branch ref to it. Returns the new
	// commit SHA. There is no force-push; a non-fast-forward is an error the
	// caller surfaces, never resolves (design D7).
	CommitFiles(ctx context.Context, branch, baseSHA string, files []FileChange, message string) (newSHA string, err error)

	// FindOpenPR returns the URL of an OPEN pull request whose head is
	// headBranch, if one exists. Composers call this first for idempotency
	// (design D7): a duplicate action returns the existing PR instead of opening
	// a second one.
	FindOpenPR(ctx context.Context, headBranch string) (url string, found bool, err error)

	// OpenPR opens a pull request from head into base and returns its URL. It
	// NEVER merges (design D3).
	OpenPR(ctx context.Context, head, base, title, body string) (url string, err error)
}
