package portalapi

import (
	"net/http"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Contributions derivation (capability portal-admin-surface, decision D4).
//
// The signed-in user's authored components are DERIVED — never stored, never
// read from a registry author field (none exists) — by walking the
// `forge-development-hub` clone's FULL commit history with the same go-git
// handle versions.go opens against FDH_PORTAL_HUB_PATH. A component
// (skills/<name>/, rules/<name>/, agents/<name>/, hooks/<name>/) is attributed
// to an author email when a commit AUTHORED by that email touched any file
// under that component's directory. "Any touching commit" counts (inclusive,
// recognition-oriented), matched case-insensitively on a trimmed email.
//
// The map is memoized by the hub HEAD commit hash: a full-history walk happens
// at most once per hub advance, so repeated profile views are cheap between
// refreshes. This is read-only; nothing is written to forge-development-hub or
// any store. The match is a documented email-match heuristic (a user's Keycloak
// email may differ from their git commit email); the web shows an empty state
// naming the email rather than an error when nothing matches.

// Contribution is one authored component, as surfaced to the web profile.
type Contribution struct {
	Kind        string `json:"kind"`         // skill|rule|agent|hook
	Name        string `json:"name"`         // the component directory name
	CommitCount int    `json:"commit_count"` // commits by this email touching the dir
	LastCommit  string `json:"last_commit"`  // RFC3339 author time of the most recent such commit
}

// contributionIndex is the memoized derivation for one hub HEAD. byEmail maps a
// normalized (lowercased, trimmed) author email to that email's contributions.
type contributionIndex struct {
	head    plumbing.Hash
	byEmail map[string][]Contribution
}

// normalizeEmail lowercases and trims an email for case-insensitive matching.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// kindFromPlural maps a top-level hub directory (skills/rules/agents/hooks) to
// the singular component kind. Returns ("", false) for any other path segment.
func kindFromPlural(plural string) (string, bool) {
	switch plural {
	case "skills":
		return "skill", true
	case "rules":
		return "rule", true
	case "agents":
		return "agent", true
	case "hooks":
		return "hook", true
	}
	return "", false
}

// componentKeyForPath maps a repo-relative slash path (e.g.
// "skills/design-system/SKILL.md") to its component (kind, name). Returns
// ("","",false) when the path is not under a recognized component directory
// (e.g. top-level files, hub/registry.yaml, .sigs/...).
func componentKeyForPath(p string) (kind, name string, ok bool) {
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	kind, ok = kindFromPlural(parts[0])
	if !ok {
		return "", "", false
	}
	name = parts[1]
	if name == "" {
		return "", "", false
	}
	return kind, name, true
}

// contributionsFor returns the components the given email authored, newest
// activity first, derived from the hub git history. The result is memoized by
// the hub HEAD commit hash; an empty/blank email or no match yields an empty
// slice (never nil-error). When the hub path is not a git repo, the result is
// empty. Safe for concurrent use.
func (s *Server) contributionsFor(email string) []Contribution {
	norm := normalizeEmail(email)
	if norm == "" {
		return []Contribution{}
	}
	idx := s.contributionIndexForHead()
	if idx == nil {
		return []Contribution{}
	}
	if c, ok := idx.byEmail[norm]; ok {
		// Defensive copy so a caller can't mutate the memoized slice.
		out := make([]Contribution, len(c))
		copy(out, c)
		return out
	}
	return []Contribution{}
}

// contributionIndexForHead returns the memoized contributionIndex for the hub's
// current HEAD, recomputing (a full-history walk) only when HEAD has advanced
// since the last computation. Returns nil when the hub path is not a git repo
// or HEAD cannot be read.
func (s *Server) contributionIndexForHead() *contributionIndex {
	repo, err := gogit.PlainOpen(s.cfg.HubPath)
	if err != nil {
		return nil // not a git repo → no contributions to derive
	}
	headRef, err := repo.Head()
	if err != nil {
		return nil
	}
	head := headRef.Hash()

	s.contribMu.Lock()
	defer s.contribMu.Unlock()
	if s.contribCache != nil && s.contribCache.head == head {
		return s.contribCache // HEAD unchanged → reuse; no re-walk
	}
	idx := buildContributionIndex(repo, head)
	s.contribCache = idx
	return idx
}

// buildContributionIndex walks the full commit history reachable from head and
// builds the email → []Contribution map. A commit attributes its author email
// to every component directory it touched (any added/modified/deleted file
// under skills/<name>/ etc.). Merge commits are skipped for attribution (their
// changes are already attributed to the side commits) to avoid double-counting.
func buildContributionIndex(repo *gogit.Repository, head plumbing.Hash) *contributionIndex {
	idx := &contributionIndex{head: head, byEmail: map[string][]Contribution{}}

	logIter, err := repo.Log(&gogit.LogOptions{From: head})
	if err != nil {
		return idx // empty index; callers treat as "no contributions"
	}

	// agg keys an (email,kind,name) tuple to its running count + latest time.
	type aggKey struct{ email, kind, name string }
	type aggVal struct {
		count int
		last  time.Time
	}
	agg := map[aggKey]*aggVal{}

	_ = logIter.ForEach(func(c *object.Commit) error {
		// Skip merge commits: their file changes are already carried by the
		// branch commits they merge, so attributing them again would
		// double-count (and a merge has no single meaningful diff base).
		if c.NumParents() > 1 {
			return nil
		}
		email := normalizeEmail(c.Author.Email)
		if email == "" {
			return nil
		}
		when := c.Author.When.UTC()

		touched, err := commitComponentDirs(c)
		if err != nil {
			return nil //nolint:nilerr // skip a commit whose diff can't be read
		}
		for ck := range touched {
			k := aggKey{email: email, kind: ck.kind, name: ck.name}
			v := agg[k]
			if v == nil {
				v = &aggVal{}
				agg[k] = v
			}
			v.count++
			if when.After(v.last) {
				v.last = when
			}
		}
		return nil
	})

	for k, v := range agg {
		idx.byEmail[k.email] = append(idx.byEmail[k.email], Contribution{
			Kind:        k.kind,
			Name:        k.name,
			CommitCount: v.count,
			LastCommit:  v.last.Format(time.RFC3339),
		})
	}
	// Stable, recognition-friendly order: most recent activity first, then by
	// kind, then name (so the list is deterministic across calls).
	for email := range idx.byEmail {
		list := idx.byEmail[email]
		sort.Slice(list, func(i, j int) bool {
			if list[i].LastCommit != list[j].LastCommit {
				return list[i].LastCommit > list[j].LastCommit
			}
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].Name < list[j].Name
		})
	}
	return idx
}

// componentDirKey identifies a touched component directory within one commit.
type componentDirKey struct{ kind, name string }

// commitComponentDirs returns the set of component directories a commit touched,
// computed by diffing the commit's tree against its (first) parent. The root
// commit (no parent) attributes every path in its tree. Paths outside the four
// component directories are ignored.
func commitComponentDirs(c *object.Commit) (map[componentDirKey]struct{}, error) {
	out := map[componentDirKey]struct{}{}

	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}

	if c.NumParents() == 0 {
		// Root commit: attribute every file present in its tree.
		err := tree.Files().ForEach(func(f *object.File) error {
			if kind, name, ok := componentKeyForPath(f.Name); ok {
				out[componentDirKey{kind: kind, name: name}] = struct{}{}
			}
			return nil
		})
		return out, err
	}

	parent, err := c.Parent(0)
	if err != nil {
		return nil, err
	}
	parentTree, err := parent.Tree()
	if err != nil {
		return nil, err
	}
	changes, err := parentTree.Diff(tree)
	if err != nil {
		return nil, err
	}
	for _, ch := range changes {
		// A change touches a path on its "from" side (delete/modify) and/or its
		// "to" side (add/modify); attribute both so a delete still counts.
		for _, name := range []string{ch.From.Name, ch.To.Name} {
			if name == "" {
				continue
			}
			if kind, cname, ok := componentKeyForPath(name); ok {
				out[componentDirKey{kind: kind, name: cname}] = struct{}{}
			}
		}
	}
	return out, nil
}

// handleGetContributions implements GET /api/v1/admin/contributions?email=<email>.
//
// It is an INTERNAL BFF surface (not in openapi.yaml): the web's server-only
// BFF calls it with the Keycloak client-credentials service token, which earns
// the `admin` role via the fdh-portal-svc role-map entry. Role-gated EXACTLY
// like handleGetActivation — 403 unless hasMinRole(u.Role, "admin"). The web
// passes ONLY the logged-in user's own email (never an arbitrary one), but the
// admin gate here is the authoritative boundary regardless.
//
// Returns 200 {"email":"<email>","contributions":[...]} with an empty list (not
// an error) for an empty email or no match — the empty-state contract (D4).
func (s *Server) handleGetContributions(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden",
			"role 'admin' required")
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	contributions := s.contributionsFor(email)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"email":         email,
		"contributions": contributions,
	})
}
