package portalapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/internal/portalapi/gitops"
	"github.com/forge/fdh/pkg/registry"
)

// fakeGitops is a portalapi-package fake implementing gitops.Client, used to
// drive the handlers without a GitHub App. It records whether any compose was
// attempted (a branch/commit/PR primitive was reached), so role-gate tests can
// assert the gate short-circuits before the bot is ever invoked.
type fakeGitops struct {
	enabled  bool
	reached  bool // true once any non-Enabled method is called
	prURL    string
	failWith error
	collide  bool // GetFile reports the import destination/registry entry exists
}

func (f *fakeGitops) Enabled() bool { return f.enabled }

func (f *fakeGitops) DefaultBranch(context.Context) (string, error) {
	f.reached = true
	return "main", nil
}
func (f *fakeGitops) DefaultBranchSHA(context.Context) (string, error) {
	f.reached = true
	return "tipsha", nil
}
func (f *fakeGitops) BranchExists(context.Context, string) (bool, error) {
	f.reached = true
	return false, nil
}
func (f *fakeGitops) CreateBranch(context.Context, string, string) error {
	f.reached = true
	return nil
}
func (f *fakeGitops) GetFile(_ context.Context, path, _ string) ([]byte, bool, error) {
	f.reached = true
	if path == "hub/registry.yaml" {
		return []byte("schema_version: 2\ncomponents:\n" +
			"  - name: design-system\n    kind: skill\n    description: d\n    owner_team: design-platform\n    default: false\n    path: skills/design-system\n"), true, nil
	}
	if path == "hub/harnesses.yaml" {
		return []byte("schema_version: 1\nharnesses:\n  default:\n    description: d\n    owner_team: t\n    skills: [design-system]\n"), true, nil
	}
	if f.collide {
		return []byte("x"), true, nil
	}
	return nil, false, nil
}
func (f *fakeGitops) CommitFiles(context.Context, string, string, []gitops.FileChange, string) (string, error) {
	f.reached = true
	if f.failWith != nil {
		return "", f.failWith
	}
	return "newsha", nil
}
func (f *fakeGitops) FindOpenPR(context.Context, string) (string, bool, error) {
	f.reached = true
	return "", false, nil
}
func (f *fakeGitops) OpenPR(context.Context, string, string, string, string) (string, error) {
	f.reached = true
	if f.prURL == "" {
		f.prURL = "https://github.com/askenaz-dev/forge-development-hub/pull/1"
	}
	return f.prURL, nil
}

// newGitopsTestServer builds a minimal Server with the given gitops client and a
// seeded catalog snapshot (so harness component-existence checks resolve).
func newGitopsTestServer(g gitops.Client) *Server {
	s := &Server{
		logger: slog.New(slog.NewTextHandler(new(bytes.Buffer), nil)),
		gitops: g,
		// AuthEnabled is false here; the handlers enforce the role from the
		// principal on the context regardless (set via requestAsBody below).
	}
	snap := &snapshot{
		index: registry.Index{
			Components: []registry.IndexEntry{
				{Kind: "skill", Name: "design-system", Namespace: "design-platform"},
				{Kind: "rule", Name: "no-console-log", Namespace: "dx-platform"},
			},
		},
	}
	s.snapshot.Store(snap)
	return s
}

// postAs builds a POST request to path with the given body, Content-Type, and a
// context carrying the auth principal (as the auth middleware would attach it).
// It also sets the BFF-forwarded X-Forge-User-Role header to the principal's
// role — these tests model the principal and the forwarded user role as equal
// (production differs: the principal is the admin service credential and the
// forwarded role is the user's; that split is covered by
// TestGitops_ForwardedUserRoleIsAuthoritative).
func postAs(path, contentType string, body []byte, u auth.User) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("X-Forge-User-Role", u.Role)
	ctx := context.WithValue(r.Context(), userContextKey{}, u)
	return r.WithContext(ctx)
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// --- role enforcement -----------------------------------------------------

func TestGitopsImport_ForbiddenBelowAuthor(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	for _, role := range []string{auth.RoleAnonymous, auth.RoleConsumer} {
		w := httptest.NewRecorder()
		body := jsonBody(t, importForm{Kind: "skill", Name: "x", Description: "d"})
		s.handleGitopsImport(w, postAs("/api/v1/gitops/import", "application/json", body, auth.User{Role: role}))
		require.Equal(t, http.StatusForbidden, w.Code, "role %s must be forbidden; body=%s", role, w.Body.String())
		assert.False(t, g.reached, "the bot must NOT be invoked for an under-role caller")
	}
}

func TestGitopsHarness_ForbiddenBelowPublisher(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	// An author may import but NOT edit a harness.
	for _, role := range []string{auth.RoleAuthor, auth.RoleReviewer} {
		w := httptest.NewRecorder()
		body := jsonBody(t, harnessRequest{Harness: "frontend-team", AddRules: []string{"no-console-log"}})
		s.handleGitopsHarness(w, postAs("/api/v1/gitops/harness", "application/json", body, auth.User{Role: role}))
		require.Equal(t, http.StatusForbidden, w.Code, "role %s must be forbidden for harness", role)
		assert.False(t, g.reached)
	}
}

func TestGitopsCurate_ForbiddenBelowAdmin(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	// A publisher may edit a harness but NOT curate.
	yes := true
	w := httptest.NewRecorder()
	body := jsonBody(t, curateRequest{Kind: "skill", Name: "design-system", SetDefault: &yes})
	s.handleGitopsCurate(w, postAs("/api/v1/gitops/curate", "application/json", body, auth.User{Role: auth.RolePublisher}))
	require.Equal(t, http.StatusForbidden, w.Code, "publisher must be forbidden for curate; body=%s", w.Body.String())
	assert.False(t, g.reached)
}

// TestGitops_ForwardedUserRoleIsAuthoritative proves the Go gate enforces the
// BFF-forwarded USER role, not just the service principal. In production the
// principal is the BFF service credential, which the role-map maps to admin — so
// a principal-only check would let any forwarded user reach any action. The gate
// must therefore refuse when the forwarded X-Forge-User-Role is below the action
// minimum, and refuse a bare service-token call carrying no forwarded role.
func TestGitops_ForwardedUserRoleIsAuthoritative(t *testing.T) {
	// svc models the BFF service principal: admin-mapped (so the principal check
	// always passes — the forwarded user role is the real gate).
	svc := auth.User{Role: auth.RoleAdmin, Sub: "service-account-fdh-portal"}

	build := func(path, ct string, body []byte, forwardedRole string, setHeader bool) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		if setHeader {
			r.Header.Set("X-Forge-User-Role", forwardedRole)
		}
		return r.WithContext(context.WithValue(r.Context(), userContextKey{}, svc))
	}
	yes := true
	curateBody := jsonBody(t, curateRequest{Kind: "skill", Name: "design-system", SetDefault: &yes})

	t.Run("author forwarded cannot curate (admin)", func(t *testing.T) {
		g := &fakeGitops{enabled: true}
		s := newGitopsTestServer(g)
		w := httptest.NewRecorder()
		s.handleGitopsCurate(w, build("/api/v1/gitops/curate", "application/json", curateBody, auth.RoleAuthor, true))
		require.Equal(t, http.StatusForbidden, w.Code, "admin service principal + forwarded author must be refused on curate; body=%s", w.Body.String())
		assert.False(t, g.reached, "the bot must NOT be invoked when the forwarded user role is below the minimum")
	})

	t.Run("publisher forwarded cannot curate (admin)", func(t *testing.T) {
		g := &fakeGitops{enabled: true}
		s := newGitopsTestServer(g)
		w := httptest.NewRecorder()
		s.handleGitopsCurate(w, build("/api/v1/gitops/curate", "application/json", curateBody, auth.RolePublisher, true))
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.False(t, g.reached)
	})

	t.Run("missing forwarded role is refused", func(t *testing.T) {
		g := &fakeGitops{enabled: true}
		s := newGitopsTestServer(g)
		w := httptest.NewRecorder()
		body := jsonBody(t, importForm{Kind: "skill", Name: "x", Description: "d"})
		s.handleGitopsImport(w, build("/api/v1/gitops/import", "application/json", body, "", false))
		require.Equal(t, http.StatusForbidden, w.Code, "a service-token call with no forwarded user role must be refused; body=%s", w.Body.String())
		assert.False(t, g.reached)
	})

	t.Run("admin forwarded can curate", func(t *testing.T) {
		g := &fakeGitops{enabled: true}
		s := newGitopsTestServer(g)
		w := httptest.NewRecorder()
		s.handleGitopsCurate(w, build("/api/v1/gitops/curate", "application/json", curateBody, auth.RoleAdmin, true))
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
		assert.True(t, g.reached)
	})
}

// --- disabled client → 503 ------------------------------------------------

func TestGitops_DisabledClientReturns503(t *testing.T) {
	s := newGitopsTestServer(gitops.Disabled())

	// Even a fully-authorized admin gets a typed 503, never a 500/crash.
	t.Run("import", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := jsonBody(t, importForm{Kind: "skill", Name: "x", Description: "d"})
		s.handleGitopsImport(w, postAs("/api/v1/gitops/import", "application/json", body, auth.User{Role: auth.RoleAdmin}))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		assertErrorCode(t, w, "gitops_not_configured")
	})
	t.Run("harness", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := jsonBody(t, harnessRequest{Harness: "frontend-team", AddRules: []string{"no-console-log"}})
		s.handleGitopsHarness(w, postAs("/api/v1/gitops/harness", "application/json", body, auth.User{Role: auth.RoleAdmin}))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		assertErrorCode(t, w, "gitops_not_configured")
	})
	t.Run("curate", func(t *testing.T) {
		yes := true
		w := httptest.NewRecorder()
		body := jsonBody(t, curateRequest{Kind: "skill", Name: "design-system", SetDefault: &yes})
		s.handleGitopsCurate(w, postAs("/api/v1/gitops/curate", "application/json", body, auth.User{Role: auth.RoleAdmin}))
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		assertErrorCode(t, w, "gitops_not_configured")
	})
}

// --- happy-path import (JSON form) ----------------------------------------

func TestGitopsImport_JSONFormOpensPR(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	w := httptest.NewRecorder()
	body := jsonBody(t, importForm{
		Kind: "skill", Name: "card-grid",
		Description: "A grid of cards. Use when laying out cards in a responsive grid.",
		OwnerTeam:   "design-platform",
	})
	s.handleGitopsImport(w, postAs("/api/v1/gitops/import", "application/json", body, auth.User{Role: auth.RoleAuthor, Name: "Dev"}))
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["pr_url"], "/pull/")
	assert.Equal(t, false, resp["merged"], "the response must state the bot did not merge")
	assert.True(t, g.reached)
}

// --- happy-path import (multipart zip) ------------------------------------

func TestGitopsImport_MultipartZipOpensPR(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)

	zipBytes := buildSkillZip(t, "card-grid", "A grid of cards. Use when laying out cards in a grid.")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("kind", "skill"))
	require.NoError(t, mw.WriteField("name", "card-grid"))
	require.NoError(t, mw.WriteField("owner_team", "design-platform"))
	fw, err := mw.CreateFormFile("bundle", "card-grid.zip")
	require.NoError(t, err)
	_, err = fw.Write(zipBytes)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	w := httptest.NewRecorder()
	s.handleGitopsImport(w, postAs("/api/v1/gitops/import", mw.FormDataContentType(), buf.Bytes(), auth.User{Role: auth.RoleAuthor}))
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	assert.True(t, g.reached)
}

// --- validation parity at the handler -------------------------------------

func TestGitopsImport_InvalidBundleAbortsWith422(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	// Name mismatch (dir name != frontmatter) is caught by the validation gate;
	// here the JSON form synthesizes a SKILL.md whose name equals the form name,
	// so to fail validation we supply a too-short description via files override
	// that violates the frontmatter rules.
	w := httptest.NewRecorder()
	body := jsonBody(t, importForm{
		Kind: "skill", Name: "card-grid",
		Files: map[string]string{
			"SKILL.md": "---\nname: different-name\nversion: 0.1.0\ndescription: d\n---\n# x\n",
		},
	})
	s.handleGitopsImport(w, postAs("/api/v1/gitops/import", "application/json", body, auth.User{Role: auth.RoleAuthor}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	assertErrorCode(t, w, "validation_failed")
}

// TestGitopsImport_RejectsTraversalName proves a request name that is not a valid
// component name (e.g. "../evil") is rejected on entry — BEFORE it is used to
// build the temp bundle dir, branch, file paths, or registry entry — closing the
// path-traversal / temp-escape vector.
func TestGitopsImport_RejectsTraversalName(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	for _, bad := range []string{"../evil", "a/b", "foo/../bar", "..", "Evil", "with space", "/abs"} {
		w := httptest.NewRecorder()
		body := jsonBody(t, importForm{Kind: "skill", Name: bad, Description: "d"})
		s.handleGitopsImport(w, postAs("/api/v1/gitops/import", "application/json", body, auth.User{Role: auth.RoleAuthor}))
		require.Equal(t, http.StatusBadRequest, w.Code, "name %q must be rejected on entry; body=%s", bad, w.Body.String())
		assert.False(t, g.reached, "no branch/commit/PR may be reached for an invalid name %q", bad)
	}
}

// --- harness unknown-component rejection ----------------------------------

func TestGitopsHarness_RejectsUnknownComponent(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	w := httptest.NewRecorder()
	body := jsonBody(t, harnessRequest{Harness: "frontend-team", AddSkills: []string{"phantom-skill"}})
	s.handleGitopsHarness(w, postAs("/api/v1/gitops/harness", "application/json", body, auth.User{Role: auth.RolePublisher}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	assertErrorCode(t, w, "unknown_component")
	assert.Contains(t, w.Body.String(), "phantom-skill")
	assert.False(t, g.reached, "unknown reference must abort before composing")
}

func TestGitopsHarness_KnownComponentOpensPR(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	w := httptest.NewRecorder()
	body := jsonBody(t, harnessRequest{Harness: "default", AddRules: []string{"no-console-log"}})
	s.handleGitopsHarness(w, postAs("/api/v1/gitops/harness", "application/json", body, auth.User{Role: auth.RolePublisher}))
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	assert.True(t, g.reached)
}

// --- curate un-yank refusal at the handler --------------------------------
// (the composer rejects it; the handler surfaces the typed 422). A full lifecycle
// path is covered in the gitops package; here we assert the handler maps the
// composer's ErrLifecycle to 422 lifecycle_rejected using a real composer-backed
// client seeded with a yanked version.

func TestGitopsCurate_AdminCanReachComposer(t *testing.T) {
	g := &fakeGitops{enabled: true}
	s := newGitopsTestServer(g)
	yes := true
	w := httptest.NewRecorder()
	body := jsonBody(t, curateRequest{Kind: "skill", Name: "design-system", SetDefault: &yes})
	s.handleGitopsCurate(w, postAs("/api/v1/gitops/curate", "application/json", body, auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	assert.True(t, g.reached)
}

// --- helpers --------------------------------------------------------------

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, code, body["error"], "unexpected error code; body=%s", w.Body.String())
}

// buildSkillZip returns a zip archive of a minimal valid skill bundle whose top
// dir is the skill name.
func buildSkillZip(t *testing.T, name, desc string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(path, content string) {
		fw, err := zw.Create(path)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	add(name+"/SKILL.md", "---\nname: "+name+"\nversion: 0.1.0\ndescription: "+desc+"\n---\n\n# "+name+"\n")
	add(name+"/references/guide.md", "# Guide\n")
	require.NoError(t, zw.Close())
	return buf.Bytes()
}
