package gitops

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ComposeHarness composes the harness-edit web action: it opens a single PR on
// a deterministic branch web/harness/<harness-name> that edits ONLY
// hub/harnesses.yaml (add/remove components per kind, edit description/
// owner_team), commit `chore(harness): update <harness-name>`.
//
// The handler is responsible for validating that every ADDED component exists in
// the live catalog before calling this (the spec's reject-unknown-reference
// rule); this composer focuses on producing a minimal, valid harnesses.yaml
// edit. Idempotency (D7) and latest-tip branching (D5) apply.
func ComposeHarness(ctx context.Context, c Client, harnessName string, edit HarnessEdit, requestor Requestor) (Result, error) {
	if !c.Enabled() {
		return Result{}, ErrGitopsNotConfigured
	}
	if edit.isEmpty() {
		return Result{}, fmt.Errorf("harness edit is empty — nothing to change")
	}

	branch := fmt.Sprintf("web/harness/%s", harnessName)

	if res, found, err := idempotentExisting(ctx, c, branch); err != nil {
		return Result{}, err
	} else if found {
		return res, nil
	}

	defBranch, err := c.DefaultBranch(ctx)
	if err != nil {
		return Result{}, err
	}
	harnessData, found, err := c.GetFile(ctx, hubHarnessesPath, defBranch)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("hub %s not found at %s", hubHarnessesPath, defBranch)
	}

	newHarness, err := applyHarnessEdit(harnessData, harnessName, edit)
	if err != nil {
		return Result{}, err
	}

	baseSHA, err := prepareBranch(ctx, c, branch)
	if err != nil {
		return Result{}, err
	}
	commitMsg := fmt.Sprintf("chore(harness): update %s", harnessName)
	files := []FileChange{{Path: hubHarnessesPath, Content: newHarness}}
	if _, err := c.CommitFiles(ctx, branch, baseSHA, files, commitMsg); err != nil {
		return Result{}, err
	}

	body := buildPRBody(
		fmt.Sprintf("Edit harness `%s` from the portal.", harnessName),
		requestor,
		editSummary(edit)...,
	)
	url, err := c.OpenPR(ctx, branch, defBranch, commitMsg, body)
	if err != nil {
		return Result{}, err
	}
	return Result{URL: url, Branch: branch}, nil
}

// editSummary renders a human bullet list of the harness mutations for the PR
// body. Deterministic ordering keeps PR bodies stable across runs.
func editSummary(e HarnessEdit) []string {
	var out []string
	if e.Description != nil {
		out = append(out, "Update description")
	}
	if e.OwnerTeam != nil {
		out = append(out, fmt.Sprintf("Set owner_team to `%s`", *e.OwnerTeam))
	}
	add := func(kind string, names []string, verb string) {
		if len(names) == 0 {
			return
		}
		s := append([]string(nil), names...)
		sort.Strings(s)
		out = append(out, fmt.Sprintf("%s %s: %s", verb, kind, strings.Join(s, ", ")))
	}
	add("skills", e.AddSkills, "Add")
	add("skills", e.RemoveSkills, "Remove")
	add("rules", e.AddRules, "Add")
	add("rules", e.RemoveRules, "Remove")
	add("agents", e.AddAgents, "Add")
	add("agents", e.RemoveAgents, "Remove")
	add("hooks", e.AddHooks, "Add")
	add("hooks", e.RemoveHooks, "Remove")
	return out
}
