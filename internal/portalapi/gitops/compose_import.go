package gitops

import (
	"context"
	"fmt"
)

// ComposeImport composes the import web action: it runs the CLI validation gate
// server-side, then opens a single PR on a deterministic branch
// web/import/<kind>/<name> that adds the bundle tree under <kind>s/<name>/ plus
// a hub/registry.yaml v2 entry (default:false, agents_supported from the
// bundle), mirroring `fdh share` (internal/cli/authoring.go runKindShare).
//
// Invariants enforced here:
//   - Validation parity + fail-fast (D4): ValidateBundleDir runs BEFORE any
//     branch/commit/PR; on failure NOTHING is pushed.
//   - Name-collision guard (D7): aborts with ErrNameCollision if <kind>s/<name>/
//     already exists in the hub OR the registry already declares the entry.
//   - Idempotency (D7): an existing open PR on the branch is returned, no dup.
//   - Always branches from the latest default-branch tip; never force-pushes.
//
// knownAgentIDs is the adapter set for the portability lint (pass the live map;
// nil falls back to DefaultAgentIDs).
func ComposeImport(ctx context.Context, c Client, kind, name, bundleDir string, meta ImportMeta, requestor Requestor, knownAgentIDs []string) (Result, error) {
	if !c.Enabled() {
		return Result{}, ErrGitopsNotConfigured
	}

	// 1. Validation gate — runs first; aborts before any push on failure.
	b, err := ValidateBundleDir(bundleDir, knownAgentIDs)
	if err != nil {
		return Result{}, err
	}

	branch := fmt.Sprintf("web/import/%s/%s", kind, name)

	// 2. Idempotency: return an existing open PR for this branch, if any.
	if res, found, err := idempotentExisting(ctx, c, branch); err != nil {
		return Result{}, err
	} else if found {
		return res, nil
	}

	// 3. Name-collision guard (mirrors the CLI copyTree abort-on-existing-dest):
	//    reject if the destination dir already exists in the hub OR the registry
	//    already has the entry. Both are checked against the default branch tip.
	defBranch, err := c.DefaultBranch(ctx)
	if err != nil {
		return Result{}, err
	}
	destPrefix := kindPlural(kind) + "/" + name
	if _, exists, err := c.GetFile(ctx, destPrefix+"/"+entrypointFilename(kind), defBranch); err != nil {
		return Result{}, err
	} else if exists {
		return Result{}, fmt.Errorf("%w: %s already exists in the hub — choose another name", ErrNameCollision, destPrefix)
	}

	regData, regFound, err := c.GetFile(ctx, hubRegistryPath, defBranch)
	if err != nil {
		return Result{}, err
	}
	if !regFound {
		return Result{}, fmt.Errorf("hub %s not found at %s", hubRegistryPath, defBranch)
	}
	if exists, err := registryComponentExists(regData, kind, name); err != nil {
		return Result{}, err
	} else if exists {
		return Result{}, fmt.Errorf("%w: registry already has a %s named %q — choose another name", ErrNameCollision, kind, name)
	}

	// 4. Assemble the atomic commit: bundle tree + appended registry entry.
	files, err := collectBundleFiles(bundleDir, destPrefix)
	if err != nil {
		return Result{}, err
	}
	agents := bundleAgents(b, meta)
	newReg := appendRegistryEntry(regData, kind, name, b.SkillMD.Description, meta.OwnerTeam, agents)
	files = append(files, FileChange{Path: hubRegistryPath, Content: newReg})

	// 5. Branch from the latest tip, commit atomically, open the PR.
	baseSHA, err := prepareBranch(ctx, c, branch)
	if err != nil {
		return Result{}, err
	}
	commitMsg := fmt.Sprintf("feat(%s): add %s", name, kind)
	if _, err := c.CommitFiles(ctx, branch, baseSHA, files, commitMsg); err != nil {
		return Result{}, err
	}

	title := fmt.Sprintf("feat(%s): add %s", name, kind)
	body := buildPRBody(
		fmt.Sprintf("Import %s `%s` from the portal.", kind, name),
		requestor,
		fmt.Sprintf("Adds `%s/` (bundle) and a `%s` entry with `default: false`.", destPrefix, hubRegistryPath),
	)
	url, err := c.OpenPR(ctx, branch, defBranch, title, body)
	if err != nil {
		return Result{}, err
	}
	return Result{URL: url, Branch: branch}, nil
}

// entrypointFilename maps a kind to its entrypoint file, mirroring the CLI. Used
// by the name-collision guard to probe an existing component directory.
func entrypointFilename(kind string) string {
	switch kind {
	case "skill":
		return "SKILL.md"
	case "rule":
		return "RULE.md"
	case "agent":
		return "AGENT.md"
	case "hook":
		return "HOOK.md"
	}
	return ""
}
