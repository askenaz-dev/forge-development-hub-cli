package portalapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/internal/portalapi/gitops"
	"github.com/forge/fdh/pkg/bundle"
)

// GitOps write endpoints (capability portal-gitops-write). Each is role-gated
// twice in the system (design D8):
//  1. the Next.js BFF resolves the user's portal role from session.user.groups
//     and refuses below the action minimum BEFORE forwarding (advisory UX);
//  2. these handlers re-enforce the minimum AUTHORITATIVELY — see requireMinRole.
//
// AUTHZ SUBTLETY: the portal→API call is authorized by the Phase-1 BFF service
// credential, which the role-map maps to `admin`. So the service PRINCIPAL is
// always admin and a principal-only HasMinRole check would NOT differentiate
// import(author) / harness(publisher) / curate(admin). The authoritative
// per-user gate therefore ALSO checks the BFF-forwarded, server-verified user
// role in the X-Forge-User-Role header — trusted ONLY because the request
// authenticated as the privileged service principal, and REQUIRED (a bare
// service-token call carrying no forwarded user role is refused). They NEVER
// accept a user IdP bearer (it is stripped from the session cookie) and never
// expose a merge path.
//
// Role minimums (precedence anonymous<consumer<author<reviewer<publisher<admin):
//   - POST /api/v1/gitops/import   → author+
//   - POST /api/v1/gitops/harness  → publisher+
//   - POST /api/v1/gitops/curate   → admin
//
// A disabled gitops client (GitHub App env absent) yields a typed 503
// gitops_not_configured with Retry-After — never a 500, never a crash
// (portal-runtime-resilience).

// Import upload / extraction bounds. maxImportZipBytes caps the COMPRESSED
// upload; maxImportZipEntries and maxImportUncompressedBytes bound zip EXPANSION
// so a small (sub-cap) archive cannot decompress to gigabytes on the API pod
// (zip-bomb / disk-exhaustion guard — the compressed cap alone does not bound the
// uncompressed total).
const (
	maxImportZipBytes          = 25 << 20  // 25 MiB compressed upload
	maxImportZipEntries        = 4096      // max files in the archive
	maxImportUncompressedBytes = 128 << 20 // 128 MiB total uncompressed across all entries
)

// requireMinRole enforces the minimum portal role for a gitops write action,
// writing a 403 forbidden envelope and returning ok=false when insufficient. It
// gates on BOTH (a) the authenticated principal and (b) the BFF-forwarded,
// server-verified user role — and returns the effective user role for PR
// attribution.
//
// Why both: the portal→API call is authorized by the service credential, which
// the role-map maps to `admin`; so the principal check alone passes for every
// action and cannot differentiate import(author)/harness(publisher)/curate(admin).
// The X-Forge-User-Role header carries the role the BFF resolved from the user's
// session (design D8). It is trusted ONLY because the request authenticated as a
// principal that itself satisfies the minimum (the privileged service principal),
// and it is REQUIRED: a service-token call with no forwarded user role — e.g. a
// future server-side caller or an SSRF that reuses the token — is refused, so the
// per-action role is genuinely enforced server-side, not only in the browser BFF.
// When auth is disabled (dev) the principal is anonymous and the gate still
// applies, so a misconfigured deploy cannot open the write surface to anonymous.
func (s *Server) requireMinRole(w http.ResponseWriter, r *http.Request, minRole string) (u auth.User, userRole string, ok bool) {
	deny := func() (auth.User, string, bool) {
		s.writeError(w, http.StatusForbidden, "forbidden",
			fmt.Sprintf("role '%s' or above required", minRole))
		return u, "", false
	}
	u = userFromRequest(r)
	if !auth.HasMinRole(u.Role, minRole) {
		return deny()
	}
	userRole = strings.TrimSpace(r.Header.Get("X-Forge-User-Role"))
	if userRole == "" || !auth.HasMinRole(userRole, minRole) {
		return deny()
	}
	return u, userRole, true
}

// requestorFor builds the trusted PR-attribution metadata from the request. The
// identity is server-verified: the name/email come from the BFF-forwarded
// headers X-Forge-User / X-Forge-User-Email and the role is the BFF-forwarded
// user role validated by requireMinRole (NOT the service principal's mapped
// `admin` role, which would mis-credit every PR as admin). All are set
// server-side by the Next.js BFF from session.user, NEVER client free-text used
// for authorization — authorization is the role gate, which is enforced
// independently.
func requestorFor(r *http.Request, u auth.User, userRole string) gitops.Requestor {
	name := strings.TrimSpace(r.Header.Get("X-Forge-User"))
	email := strings.TrimSpace(r.Header.Get("X-Forge-User-Email"))
	if name == "" {
		name = u.Name
	}
	if email == "" {
		email = u.Email
	}
	if strings.TrimSpace(userRole) == "" {
		userRole = u.Role
	}
	return gitops.Requestor{Name: name, Email: email, Role: userRole}
}

// gitopsResult writes the standard success envelope carrying the PR URL. When
// the action was idempotent (an open PR already existed), it returns 200 with
// already_open=true; a freshly-opened PR returns 201.
func (s *Server) gitopsResult(w http.ResponseWriter, res gitops.Result) {
	status := http.StatusCreated
	if res.AlreadyOpen {
		status = http.StatusOK
	}
	s.writeJSON(w, status, map[string]any{
		"pr_url":       res.URL,
		"branch":       res.Branch,
		"already_open": res.AlreadyOpen,
		"merged":       false, // the bot is propose-only; it never merges (D3).
	})
}

// writeGitopsError maps a composer/client error to the right typed HTTP status:
// disabled client → 503; name collision / validation / lifecycle → 422; else 500.
func (s *Server) writeGitopsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitops.ErrGitopsNotConfigured):
		w.Header().Set("Retry-After", "30")
		s.writeError(w, http.StatusServiceUnavailable, "gitops_not_configured",
			"the portal GitHub App is not configured; the web write surface is unavailable")
	case errors.Is(err, gitops.ErrNameCollision):
		s.writeError(w, http.StatusUnprocessableEntity, "name_collision", err.Error())
	case errors.Is(err, gitops.ErrBranchConflict):
		// A stale deterministic branch (no open PR) blocks this action; the
		// message is our own (no upstream content) and the state is recoverable
		// by deleting the branch, so surface it as a typed, actionable 409.
		s.writeError(w, http.StatusConflict, "branch_conflict", err.Error())
	default:
		var ve *gitops.ErrValidation
		var le *gitops.ErrLifecycle
		switch {
		case errors.As(err, &ve):
			s.writeError(w, http.StatusUnprocessableEntity, "validation_failed", ve.Error())
		case errors.As(err, &le):
			s.writeError(w, http.StatusUnprocessableEntity, "lifecycle_rejected", le.Error())
		default:
			// Do NOT echo the raw upstream error: a GitHub apiError embeds the
			// response body and the owner/repo path. Log it server-side and
			// return a generic message (information-disclosure guard).
			s.logger.Error("gitops operation failed", "err", err.Error())
			s.writeError(w, http.StatusInternalServerError, "gitops_failed",
				"the GitHub write operation failed; please retry or contact an administrator")
		}
	}
}

// --- import ---------------------------------------------------------------

// importForm is the JSON skill-form variant of an import request. The zip
// variant carries the bundle as a multipart file. Either way, the trusted user
// metadata is resolved server-side (not from this body).
type importForm struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	OwnerTeam   string   `json:"owner_team"`
	Agents      []string `json:"agents,omitempty"`
	// Files maps a bundle-relative path → file content for a form-built bundle.
	// Must include the kind's entrypoint (e.g. SKILL.md). Used when no zip is
	// uploaded.
	Files map[string]string `json:"files,omitempty"`
}

// handleGitopsImport implements POST /api/v1/gitops/import (author+). It accepts
// either a multipart zip upload (field "bundle") or a JSON skill-form, unzips/
// materializes it to a temp dir, runs the server-side validation gate, and
// composes the import PR. The temp dir is always cleaned up.
func (s *Server) handleGitopsImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportZipBytes)
	u, userRole, ok := s.requireMinRole(w, r, auth.RoleAuthor)
	if !ok {
		return
	}
	if !s.gitops.Enabled() {
		s.writeGitopsError(w, gitops.ErrGitopsNotConfigured)
		return
	}

	tmpDir, err := os.MkdirTemp("", "fdh-import-*")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "import_failed", "could not allocate a temp dir")
		return
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	var (
		kind, name string
		meta       gitops.ImportMeta
		bundleDir  string
	)

	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		kind, name, meta, bundleDir, err = s.importFromMultipart(r, tmpDir)
	default:
		kind, name, meta, bundleDir, err = s.importFromJSON(r, tmpDir)
	}
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !isImportableKind(kind) {
		s.writeError(w, http.StatusUnprocessableEntity, "unsupported_kind",
			fmt.Sprintf("kind %q is not importable from the web yet (skill is supported)", kind))
		return
	}

	res, err := gitops.ComposeImport(r.Context(), s.gitops, kind, name, bundleDir, meta, requestorFor(r, u, userRole), s.knownAgentIDs())
	if err != nil {
		s.writeGitopsError(w, err)
		return
	}
	s.gitopsResult(w, res)
}

// importFromMultipart reads the "bundle" zip file (plus kind/name/owner_team
// form fields), unzips it under tmpDir, and returns the bundle dir. The bundle
// is extracted into tmpDir/<name>/ so bundle.Load sees the canonical directory
// name (which must equal the frontmatter name).
func (s *Server) importFromMultipart(r *http.Request, tmpDir string) (kind, name string, meta gitops.ImportMeta, bundleDir string, err error) {
	if err = r.ParseMultipartForm(maxImportZipBytes); err != nil {
		return "", "", meta, "", fmt.Errorf("parse multipart form: %w", err)
	}
	kind = strings.TrimSpace(r.FormValue("kind"))
	name = strings.TrimSpace(r.FormValue("name"))
	meta.OwnerTeam = strings.TrimSpace(r.FormValue("owner_team"))
	if a := strings.TrimSpace(r.FormValue("agents")); a != "" {
		meta.Agents = splitCSV(a)
	}
	if kind == "" || name == "" {
		return "", "", meta, "", fmt.Errorf("kind and name are required")
	}
	if err = validComponentName(name); err != nil {
		return "", "", meta, "", err
	}

	file, hdr, ferr := r.FormFile("bundle")
	if ferr != nil {
		return "", "", meta, "", fmt.Errorf("missing 'bundle' zip upload: %w", ferr)
	}
	defer func() { _ = file.Close() }()
	if hdr.Size > maxImportZipBytes {
		return "", "", meta, "", fmt.Errorf("bundle exceeds the %d-byte upload cap", maxImportZipBytes)
	}

	zipBytes, rerr := io.ReadAll(io.LimitReader(file, maxImportZipBytes+1))
	if rerr != nil {
		return "", "", meta, "", fmt.Errorf("read upload: %w", rerr)
	}
	if len(zipBytes) > maxImportZipBytes {
		return "", "", meta, "", fmt.Errorf("bundle exceeds the %d-byte upload cap", maxImportZipBytes)
	}

	bundleDir = filepath.Join(tmpDir, name)
	if err = unzipInto(zipBytes, bundleDir); err != nil {
		return "", "", meta, "", err
	}
	return kind, name, meta, bundleDir, nil
}

// importFromJSON reads the skill-form JSON body and materializes a bundle dir
// under tmpDir/<name>/ from the supplied files. When a description is given but
// no explicit SKILL.md, a minimal entrypoint is synthesized so the bundle loads;
// when files include the entrypoint, they are written verbatim.
func (s *Server) importFromJSON(r *http.Request, tmpDir string) (kind, name string, meta gitops.ImportMeta, bundleDir string, err error) {
	var form importForm
	dec := json.NewDecoder(io.LimitReader(r.Body, maxImportZipBytes))
	dec.DisallowUnknownFields()
	if derr := dec.Decode(&form); derr != nil {
		return "", "", meta, "", fmt.Errorf("decode JSON skill form: %w", derr)
	}
	kind = strings.TrimSpace(form.Kind)
	name = strings.TrimSpace(form.Name)
	meta.OwnerTeam = strings.TrimSpace(form.OwnerTeam)
	meta.Agents = form.Agents
	if kind == "" || name == "" {
		return "", "", meta, "", fmt.Errorf("kind and name are required")
	}
	if err = validComponentName(name); err != nil {
		return "", "", meta, "", err
	}

	bundleDir = filepath.Join(tmpDir, name)
	if mkErr := os.MkdirAll(bundleDir, 0o755); mkErr != nil {
		return "", "", meta, "", mkErr
	}

	entrypoint := entrypointFile(kind)
	wroteEntrypoint := false
	for rel, content := range form.Files {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return "", "", meta, "", fmt.Errorf("illegal file path in form: %q", rel)
		}
		dest := filepath.Join(bundleDir, clean)
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return "", "", meta, "", mkErr
		}
		if wErr := os.WriteFile(dest, []byte(content), 0o644); wErr != nil {
			return "", "", meta, "", wErr
		}
		if filepath.Base(clean) == entrypoint {
			wroteEntrypoint = true
		}
	}
	if !wroteEntrypoint {
		// Synthesize a minimal entrypoint from name+description so the bundle
		// loads and validates (mirrors the CLI scaffold frontmatter).
		if strings.TrimSpace(form.Description) == "" {
			return "", "", meta, "", fmt.Errorf("provide files including %s, or a description to synthesize one", entrypoint)
		}
		body := fmt.Sprintf("---\nname: %s\nversion: 0.1.0\ndescription: %s\n---\n\n# %s\n", name, form.Description, name)
		if wErr := os.WriteFile(filepath.Join(bundleDir, entrypoint), []byte(body), 0o644); wErr != nil {
			return "", "", meta, "", wErr
		}
	}
	return kind, name, meta, bundleDir, nil
}

// --- harness --------------------------------------------------------------

// harnessRequest is the JSON body for a harness edit. Add/remove are per kind.
type harnessRequest struct {
	Harness      string   `json:"harness"`
	Description  *string  `json:"description,omitempty"`
	OwnerTeam    *string  `json:"owner_team,omitempty"`
	AddSkills    []string `json:"add_skills,omitempty"`
	RemoveSkills []string `json:"remove_skills,omitempty"`
	AddRules     []string `json:"add_rules,omitempty"`
	RemoveRules  []string `json:"remove_rules,omitempty"`
	AddAgents    []string `json:"add_agents,omitempty"`
	RemoveAgents []string `json:"remove_agents,omitempty"`
	AddHooks     []string `json:"add_hooks,omitempty"`
	RemoveHooks  []string `json:"remove_hooks,omitempty"`
}

// handleGitopsHarness implements POST /api/v1/gitops/harness (publisher+). It
// validates every ADDED component exists in the live catalog snapshot (rejecting
// unknown references, naming them) BEFORE composing, then opens a harness PR
// touching only hub/harnesses.yaml.
func (s *Server) handleGitopsHarness(w http.ResponseWriter, r *http.Request) {
	u, userRole, ok := s.requireMinRole(w, r, auth.RolePublisher)
	if !ok {
		return
	}
	if !s.gitops.Enabled() {
		s.writeGitopsError(w, gitops.ErrGitopsNotConfigured)
		return
	}

	var req harnessRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Harness) == "" {
		s.writeError(w, http.StatusBadRequest, "bad_request", "harness name is required")
		return
	}
	if err := validComponentName(strings.TrimSpace(req.Harness)); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	edit := gitops.HarnessEdit{
		Description:  req.Description,
		OwnerTeam:    req.OwnerTeam,
		AddSkills:    req.AddSkills,
		RemoveSkills: req.RemoveSkills,
		AddRules:     req.AddRules,
		RemoveRules:  req.RemoveRules,
		AddAgents:    req.AddAgents,
		RemoveAgents: req.RemoveAgents,
		AddHooks:     req.AddHooks,
		RemoveHooks:  req.RemoveHooks,
	}

	// Reject references to non-existent components BEFORE composing, so the
	// harness validation cannot fail in CI (spec scenario).
	if missing := s.unknownComponents(edit); len(missing) > 0 {
		s.writeError(w, http.StatusUnprocessableEntity, "unknown_component",
			"these components are not in the catalog: "+strings.Join(missing, ", "))
		return
	}

	res, err := gitops.ComposeHarness(r.Context(), s.gitops, req.Harness, edit, requestorFor(r, u, userRole))
	if err != nil {
		s.writeGitopsError(w, err)
		return
	}
	s.gitopsResult(w, res)
}

// --- curate ---------------------------------------------------------------

// curateRequest is the JSON body for a curate action (admin). Exactly one of
// set_default / lifecycle is taken.
type curateRequest struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	SetDefault *bool  `json:"set_default,omitempty"`
	// Lifecycle is "deprecate" or "yank"; Version is required for those.
	Lifecycle string `json:"lifecycle,omitempty"`
	Version   string `json:"version,omitempty"`
}

// handleGitopsCurate implements POST /api/v1/gitops/curate (admin). It validates
// the forward-only lifecycle and computes the default-harness sync (in the
// composer), then opens a curate PR editing hub/registry.yaml (and the default
// harness atomically when default flips).
func (s *Server) handleGitopsCurate(w http.ResponseWriter, r *http.Request) {
	u, userRole, ok := s.requireMinRole(w, r, auth.RoleAdmin)
	if !ok {
		return
	}
	if !s.gitops.Enabled() {
		s.writeGitopsError(w, gitops.ErrGitopsNotConfigured)
		return
	}

	var req curateRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Kind) == "" || strings.TrimSpace(req.Name) == "" {
		s.writeError(w, http.StatusBadRequest, "bad_request", "kind and name are required")
		return
	}
	if err := validComponentName(strings.TrimSpace(req.Name)); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	action := gitops.CurateAction{SetDefault: req.SetDefault, Version: req.Version}
	switch strings.ToLower(strings.TrimSpace(req.Lifecycle)) {
	case "":
		// default-flag change (SetDefault must be set; composer validates).
	case "deprecate", "deprecated":
		action.Lifecycle = "deprecated"
	case "yank", "yanked":
		action.Lifecycle = "yanked"
	default:
		s.writeError(w, http.StatusUnprocessableEntity, "lifecycle_rejected",
			fmt.Sprintf("unsupported lifecycle %q (use deprecate or yank)", req.Lifecycle))
		return
	}

	res, err := gitops.ComposeCurate(r.Context(), s.gitops, req.Kind, req.Name, action, requestorFor(r, u, userRole))
	if err != nil {
		s.writeGitopsError(w, err)
		return
	}
	s.gitopsResult(w, res)
}

// --- helpers --------------------------------------------------------------

// knownAgentIDs returns the adapter ids used to cross-check a non-portable
// bundle's compatibility list during the import portability lint. The hub
// recognizes the canonical four; this mirrors gitops.DefaultAgentIDs so the web
// import and the CLI share the same lint surface.
func (s *Server) knownAgentIDs() []string {
	return gitops.DefaultAgentIDs
}

// unknownComponents returns the "kind:name" references in the edit that do NOT
// exist in the live catalog snapshot. Only ADDED components are checked (removes
// of absent names are harmless no-ops).
func (s *Server) unknownComponents(edit gitops.HarnessEdit) []string {
	snap := s.Snapshot()
	exists := func(kind, name string) bool {
		if snap == nil {
			return false
		}
		for _, e := range snap.index.Components {
			if e.Kind == kind && e.Name == name {
				return true
			}
		}
		return false
	}
	var missing []string
	check := func(kind string, names []string) {
		for _, n := range names {
			if !exists(kind, n) {
				missing = append(missing, kind+":"+n)
			}
		}
	}
	check("skill", edit.AddSkills)
	check("rule", edit.AddRules)
	check("agent", edit.AddAgents)
	check("hook", edit.AddHooks)
	return missing
}

// validComponentName guards a request-supplied component name BEFORE it is used to
// build the temp bundle dir, the deterministic branch, file paths, and the
// registry entry. It enforces the same NameRegex the bundle validator applies to
// the frontmatter name, so a malicious value (e.g. "../evil") is rejected on
// entry rather than escaping the temp dir or injecting into a branch/path.
func validComponentName(name string) error {
	if !bundle.NameRegex.MatchString(name) {
		return fmt.Errorf("name %q is invalid (must match %s)", name, bundle.NameRegex.String())
	}
	return nil
}

// isImportableKind reports whether the kind can be imported from the web today.
// Skills-first (design D9); other kinds follow once their materialization is
// exercised. The endpoint contract is kind-parameterized so this is a config
// gate, not a new endpoint.
func isImportableKind(kind string) bool {
	return kind == "skill"
}

// splitCSV splits a comma-separated list, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// unzipInto extracts a zip archive into destDir, guarding against zip-slip and
// flattening a single top-level wrapper directory so a bundle zipped as
// "<name>/SKILL.md" or as "SKILL.md" both land directly under destDir.
func unzipInto(zipBytes []byte, destDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if len(zr.File) > maxImportZipEntries {
		return fmt.Errorf("zip has too many entries (%d > %d)", len(zr.File), maxImportZipEntries)
	}

	prefix := commonZipPrefix(zr)

	var totalWritten int64
	for _, f := range zr.File {
		rel := f.Name
		if prefix != "" {
			rel = strings.TrimPrefix(rel, prefix)
		}
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		// Skip dotfiles/dirs (mirrors copyTree's bundle hygiene).
		clean := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("illegal path in zip: %q", f.Name)
		}
		if hasDotSegment(clean) {
			continue
		}
		dest := filepath.Join(destDir, clean)
		// Final zip-slip guard.
		if !strings.HasPrefix(dest, filepath.Clean(destDir)+string(os.PathSeparator)) && dest != filepath.Clean(destDir) {
			return fmt.Errorf("zip entry escapes destination: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		remaining := maxImportUncompressedBytes - totalWritten
		if remaining <= 0 {
			return fmt.Errorf("zip expands beyond the %d-byte uncompressed budget", maxImportUncompressedBytes)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return oerr
		}
		// Read at most the remaining budget (+1 to detect overflow). This bounds
		// BOTH a single huge entry and the cumulative total across entries.
		data, rerr := io.ReadAll(io.LimitReader(rc, remaining+1))
		_ = rc.Close()
		if rerr != nil {
			return rerr
		}
		if int64(len(data)) > remaining {
			return fmt.Errorf("zip expands beyond the %d-byte uncompressed budget", maxImportUncompressedBytes)
		}
		totalWritten += int64(len(data))
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// commonZipPrefix returns the single top-level directory shared by every entry,
// or "" if entries are not all under one wrapper dir.
func commonZipPrefix(zr *zip.Reader) string {
	var prefix string
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		if name == "" {
			continue
		}
		idx := strings.Index(name, "/")
		if idx < 0 {
			return "" // a top-level file exists → no common wrapper
		}
		top := name[:idx+1]
		if prefix == "" {
			prefix = top
		} else if prefix != top {
			return ""
		}
	}
	return prefix
}

// hasDotSegment reports whether any path segment begins with "." (a dotfile or
// dotdir), so the extractor can skip it like copyTree does.
func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
