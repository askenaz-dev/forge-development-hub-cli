package gitops

import (
	"context"
	"fmt"
)

// CurateAction is the discriminated curate request. Exactly one of the actions
// is taken per call:
//
//   - SetDefault: flip a component's `default` flag (Gate 3). When non-nil, the
//     SAME commit also adds/removes the component in the `default` harness in
//     hub/harnesses.yaml atomically (D6), so CI's cross-reference stays green.
//   - Deprecate / Yank: drive the forward-only lifecycle on a specific version.
//     Un-yank (yanked→active) and any backward transition is REJECTED.
type CurateAction struct {
	// SetDefault, when non-nil, sets registry default to *SetDefault.
	SetDefault *bool
	// Lifecycle, when non-empty, is the target status ("deprecated" or "yanked")
	// for Version. Active is never a target (forward-only).
	Lifecycle string
	// Version is required for a Lifecycle transition (e.g. "0.4.0").
	Version string
}

// ComposeCurate composes the curate web action (admin): it opens a single PR on
// a deterministic branch web/curate/<kind>/<name> editing hub/registry.yaml. For
// a default-flag change it ALSO edits the `default` harness in
// hub/harnesses.yaml in the SAME atomic commit (D6). Lifecycle transitions are
// forward-only (active→deprecated→yanked); un-yank is rejected with
// *ErrLifecycle before any push. Idempotency (D7) and latest-tip branching (D5)
// apply.
func ComposeCurate(ctx context.Context, c Client, kind, name string, action CurateAction, requestor Requestor) (Result, error) {
	if !c.Enabled() {
		return Result{}, ErrGitopsNotConfigured
	}
	if action.SetDefault == nil && action.Lifecycle == "" {
		return Result{}, fmt.Errorf("curate action is empty — set default or a lifecycle transition")
	}
	if action.SetDefault != nil && action.Lifecycle != "" {
		return Result{}, fmt.Errorf("curate action ambiguous — set default OR a lifecycle transition, not both")
	}

	branch := fmt.Sprintf("web/curate/%s/%s", kind, name)

	if res, found, err := idempotentExisting(ctx, c, branch); err != nil {
		return Result{}, err
	} else if found {
		return res, nil
	}

	defBranch, err := c.DefaultBranch(ctx)
	if err != nil {
		return Result{}, err
	}
	regData, found, err := c.GetFile(ctx, hubRegistryPath, defBranch)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("hub %s not found at %s", hubRegistryPath, defBranch)
	}

	var files []FileChange
	var commitMsg string
	var bodyDetails []string

	switch {
	case action.SetDefault != nil:
		value := *action.SetDefault
		newReg, _, err := setRegistryDefault(regData, kind, name, value)
		if err != nil {
			return Result{}, err
		}
		files = append(files, FileChange{Path: hubRegistryPath, Content: newReg})

		// D6: atomically keep the `default` harness mirroring the default:true set.
		harnessData, hFound, err := c.GetFile(ctx, hubHarnessesPath, defBranch)
		if err != nil {
			return Result{}, err
		}
		if !hFound {
			return Result{}, fmt.Errorf("hub %s not found at %s", hubHarnessesPath, defBranch)
		}
		already, err := harnessHasComponent(harnessData, "default", kind, name)
		if err != nil {
			return Result{}, err
		}
		// Only edit the harness when membership must actually change (add when
		// turning default on and not yet a member; remove when turning off and
		// currently a member). Keeps the diff minimal and avoids a no-op commit.
		if value != already {
			newHarness, err := syncDefaultHarness(harnessData, kind, name, value)
			if err != nil {
				return Result{}, err
			}
			files = append(files, FileChange{Path: hubHarnessesPath, Content: newHarness})
		}

		commitMsg = fmt.Sprintf("chore(%s): set default %t", name, value)
		verb := "Add to"
		if !value {
			verb = "Remove from"
		}
		bodyDetails = []string{
			fmt.Sprintf("Set `default: %t` on the `%s` %s entry in `%s`.", value, name, kind, hubRegistryPath),
			fmt.Sprintf("%s the `default` harness in `%s` (kept in sync atomically).", verb, hubHarnessesPath),
		}

	default: // lifecycle transition
		target := action.Lifecycle
		if target != statusDeprecated && target != statusYanked {
			return Result{}, &ErrLifecycle{Detail: fmt.Sprintf("unsupported lifecycle target %q (only deprecate/yank are allowed)", target)}
		}
		if action.Version == "" {
			return Result{}, &ErrLifecycle{Detail: "a version is required for a lifecycle transition"}
		}
		// Probe current status to enforce forward-only BEFORE editing.
		_, prior, err := setVersionStatus(regData, kind, name, action.Version, target)
		if err != nil {
			return Result{}, err
		}
		if !lifecycleAllowed(prior, target) {
			from := prior
			if from == "" {
				from = statusActive
			}
			return Result{}, &ErrLifecycle{Detail: fmt.Sprintf("forward-only lifecycle: cannot move %s/%s@%s from %s to %s", kind, name, action.Version, from, target)}
		}
		newReg, _, err := setVersionStatus(regData, kind, name, action.Version, target)
		if err != nil {
			return Result{}, err
		}
		files = append(files, FileChange{Path: hubRegistryPath, Content: newReg})

		shortVerb := "deprecate"
		if target == statusYanked {
			shortVerb = "yank"
		}
		commitMsg = fmt.Sprintf("chore(%s): %s@%s", name, shortVerb, action.Version)
		bodyDetails = []string{
			fmt.Sprintf("Set `status: %s` on `%s/%s@%s` in `%s`.", target, kindPlural(kind), name, action.Version, hubRegistryPath),
		}
	}

	baseSHA, err := prepareBranch(ctx, c, branch)
	if err != nil {
		return Result{}, err
	}
	if _, err := c.CommitFiles(ctx, branch, baseSHA, files, commitMsg); err != nil {
		return Result{}, err
	}

	body := buildPRBody(
		fmt.Sprintf("Curate %s `%s` from the portal.", kind, name),
		requestor,
		bodyDetails...,
	)
	url, err := c.OpenPR(ctx, branch, defBranch, commitMsg, body)
	if err != nil {
		return Result{}, err
	}
	return Result{URL: url, Branch: branch}, nil
}
