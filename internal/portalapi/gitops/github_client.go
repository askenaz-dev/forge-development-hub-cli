package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

// Config carries the GitHub App parameters. It is populated from the
// environment by ConfigFromEnv. When the App is not configured (any required
// field empty), New returns a disabled client.
type Config struct {
	// AppID is the GitHub App's numeric id (GITHUB_APP_ID).
	AppID int64
	// InstallationID is the App installation's numeric id on the hub repo
	// (GITHUB_APP_INSTALLATION_ID).
	InstallationID int64
	// PrivateKeyPEM is the App's RSA private key in PEM. The source env may be
	// raw PEM or base64-encoded PEM (GITHUB_APP_PRIVATE_KEY); ConfigFromEnv
	// decodes base64 transparently.
	PrivateKeyPEM []byte
	// Owner / Repo identify the single hub repository the App is installed on
	// (GITHUB_HUB_OWNER / GITHUB_HUB_REPO). The App is single-repo scoped.
	Owner string
	Repo  string
	// APIBaseURL is the GitHub REST API root, default "https://api.github.com".
	// Overridable for GHES or tests.
	APIBaseURL string
}

// Configured reports whether every required field is present.
func (c Config) Configured() bool {
	return c.AppID != 0 &&
		c.InstallationID != 0 &&
		len(c.PrivateKeyPEM) > 0 &&
		c.Owner != "" &&
		c.Repo != ""
}

// ConfigFromEnv reads the GitHub App configuration from the environment. It
// never errors on a missing App: an absent/partial config yields a Config whose
// Configured() is false, so New returns a disabled client and the API still
// boots (portal-runtime-resilience). It DOES return an error only for a present
// but malformed numeric/base64 value, so a misconfiguration is visible.
//
// Env vars:
//
//	GITHUB_APP_ID               App id (integer)
//	GITHUB_APP_INSTALLATION_ID  Installation id (integer)
//	GITHUB_APP_PRIVATE_KEY      RSA private key, raw PEM or base64-encoded PEM
//	GITHUB_HUB_OWNER            Repo owner    (default "askenaz-dev")
//	GITHUB_HUB_REPO             Repo name     (default "forge-development-hub")
//	GITHUB_API_BASE_URL         REST API root (default "https://api.github.com")
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Owner:      envOr("GITHUB_HUB_OWNER", "askenaz-dev"),
		Repo:       envOr("GITHUB_HUB_REPO", "forge-development-hub"),
		APIBaseURL: strings.TrimRight(envOr("GITHUB_API_BASE_URL", "https://api.github.com"), "/"),
	}

	if v := strings.TrimSpace(os.Getenv("GITHUB_APP_ID")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("GITHUB_APP_ID is not an integer: %w", err)
		}
		cfg.AppID = id
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALLATION_ID")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("GITHUB_APP_INSTALLATION_ID is not an integer: %w", err)
		}
		cfg.InstallationID = id
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY")); v != "" {
		pem, err := decodePEM(v)
		if err != nil {
			return cfg, fmt.Errorf("GITHUB_APP_PRIVATE_KEY: %w", err)
		}
		cfg.PrivateKeyPEM = pem
	}
	return cfg, nil
}

// decodePEM accepts either raw PEM text or base64-encoded PEM and returns raw
// PEM bytes. A value beginning with "-----BEGIN" is treated as raw PEM.
func decodePEM(v string) ([]byte, error) {
	if strings.HasPrefix(strings.TrimSpace(v), "-----BEGIN") {
		return []byte(v), nil
	}
	// Tolerate whitespace/newlines a Secret mount may introduce.
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, v)
	raw, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("value is neither raw PEM nor valid base64: %w", err)
	}
	return raw, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// githubClient is the real Client backed by a GitHub App installation token.
// It talks the GitHub Git Data REST API directly over net/http; the
// ghinstallation transport injects (and refreshes) the short-lived installation
// token on every request. It implements ONLY propose-only primitives — there is
// no merge call anywhere.
type githubClient struct {
	http    *http.Client
	apiBase string
	owner   string
	repo    string
}

// New constructs a Client from cfg. When cfg.Configured() is false it returns a
// DISABLED client (Enabled()==false, every method returns
// ErrGitopsNotConfigured) and a nil error — construction NEVER fails the boot
// path for a missing App. A malformed private key (present but unparseable) is a
// real misconfiguration and returns an error.
func New(cfg Config) (Client, error) {
	if !cfg.Configured() {
		return Disabled(), nil
	}
	base := cfg.APIBaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	// ghinstallation mints + caches the installation token and signs the App JWT.
	tr, err := ghinstallation.New(http.DefaultTransport, cfg.AppID, cfg.InstallationID, cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("gitops: construct App installation transport: %w", err)
	}
	tr.BaseURL = base
	return &githubClient{
		http:    &http.Client{Transport: tr, Timeout: 30 * time.Second},
		apiBase: strings.TrimRight(base, "/"),
		owner:   cfg.Owner,
		repo:    cfg.Repo,
	}, nil
}

// NewFromEnv is the production constructor: it reads the App config from the
// environment and builds the client. A missing App yields a disabled client
// (nil error); a malformed-but-present value yields an error.
func NewFromEnv() (Client, error) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return New(cfg)
}

func (g *githubClient) Enabled() bool { return true }

func (g *githubClient) repoURL(suffix string) string {
	return fmt.Sprintf("%s/repos/%s/%s%s", g.apiBase, g.owner, g.repo, suffix)
}

// do issues a GitHub REST request with JSON in/out. A nil `in` sends no body. It
// returns the raw response body and decodes it into `out` (if non-nil) on a 2xx.
// Non-2xx responses become a typed apiError naming the status and GitHub message.
func (g *githubClient) do(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("gitops: marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("gitops: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("gitops: %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Method: method, URL: url, Status: resp.StatusCode, Body: respBody}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("gitops: decode response from %s: %w", url, err)
		}
	}
	return nil
}

// apiError carries a non-2xx GitHub REST response.
type apiError struct {
	Method string
	URL    string
	Status int
	Body   []byte
}

func (e *apiError) Error() string {
	msg := strings.TrimSpace(string(e.Body))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Sprintf("gitops: %s %s -> HTTP %d: %s", e.Method, e.URL, e.Status, msg)
}

func (g *githubClient) DefaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.do(ctx, http.MethodGet, g.repoURL(""), nil, &repo); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "main", nil
	}
	return repo.DefaultBranch, nil
}

func (g *githubClient) DefaultBranchSHA(ctx context.Context) (string, error) {
	branch, err := g.DefaultBranch(ctx)
	if err != nil {
		return "", err
	}
	return g.refSHA(ctx, "heads/"+branch)
}

// refSHA resolves a ref (e.g. "heads/main") to its commit SHA.
func (g *githubClient) refSHA(ctx context.Context, ref string) (string, error) {
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := g.do(ctx, http.MethodGet, g.repoURL("/git/ref/"+ref), nil, &out); err != nil {
		return "", err
	}
	return out.Object.SHA, nil
}

func (g *githubClient) BranchExists(ctx context.Context, name string) (bool, error) {
	err := g.do(ctx, http.MethodGet, g.repoURL("/git/ref/heads/"+name), nil, nil)
	if err == nil {
		return true, nil
	}
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

func (g *githubClient) CreateBranch(ctx context.Context, name, fromSHA string) error {
	in := map[string]string{
		"ref": "refs/heads/" + name,
		"sha": fromSHA,
	}
	return g.do(ctx, http.MethodPost, g.repoURL("/git/refs"), in, nil)
}

func (g *githubClient) GetFile(ctx context.Context, path, ref string) ([]byte, bool, error) {
	url := g.repoURL("/contents/" + path)
	if ref != "" {
		url += "?ref=" + ref
	}
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	err := g.do(ctx, http.MethodGet, url, nil, &out)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	if out.Encoding == "base64" {
		// GitHub wraps base64 content at 60 columns.
		decoded, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
		if derr != nil {
			return nil, false, fmt.Errorf("gitops: decode %s: %w", path, derr)
		}
		return decoded, true, nil
	}
	return []byte(out.Content), true, nil
}

// CommitFiles performs the atomic Git Data dance: create a blob per add/update,
// build a tree based on baseSHA's tree with deletes nulled out, create a commit
// parented on baseSHA, then fast-forward the branch ref to the new commit. No
// force flag is ever sent.
func (g *githubClient) CommitFiles(ctx context.Context, branch, baseSHA string, files []FileChange, message string) (string, error) {
	// Resolve the base commit's tree.
	var baseCommit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := g.do(ctx, http.MethodGet, g.repoURL("/git/commits/"+baseSHA), nil, &baseCommit); err != nil {
		return "", err
	}

	// Build tree entries. A nil "sha" on an existing path deletes it from the
	// new tree; a blob sha adds/updates it. All entries are regular files
	// (mode 100644).
	entries := make([]map[string]any, 0, len(files))
	for _, f := range files {
		if f.Delete {
			// A nil sha on an existing path deletes it from the new tree.
			entries = append(entries, map[string]any{
				"path": f.Path,
				"mode": "100644",
				"type": "blob",
				"sha":  nil,
			})
			continue
		}
		blobSHA, err := g.createBlob(ctx, f.Content)
		if err != nil {
			return "", err
		}
		entries = append(entries, map[string]any{
			"path": f.Path,
			"mode": "100644",
			"type": "blob",
			"sha":  blobSHA,
		})
	}

	// Create the tree based on the base tree.
	var newTree struct {
		SHA string `json:"sha"`
	}
	treeReq := map[string]any{
		"base_tree": baseCommit.Tree.SHA,
		"tree":      entries,
	}
	if err := g.do(ctx, http.MethodPost, g.repoURL("/git/trees"), treeReq, &newTree); err != nil {
		return "", err
	}

	// Create the commit parented on the base.
	var newCommit struct {
		SHA string `json:"sha"`
	}
	commitReq := map[string]any{
		"message": message,
		"tree":    newTree.SHA,
		"parents": []string{baseSHA},
	}
	if err := g.do(ctx, http.MethodPost, g.repoURL("/git/commits"), commitReq, &newCommit); err != nil {
		return "", err
	}

	// Fast-forward the branch ref to the new commit (no force).
	updateReq := map[string]any{
		"sha":   newCommit.SHA,
		"force": false,
	}
	if err := g.do(ctx, http.MethodPatch, g.repoURL("/git/refs/heads/"+branch), updateReq, nil); err != nil {
		return "", err
	}
	return newCommit.SHA, nil
}

func (g *githubClient) createBlob(ctx context.Context, content []byte) (string, error) {
	in := map[string]string{
		"content":  base64.StdEncoding.EncodeToString(content),
		"encoding": "base64",
	}
	var out struct {
		SHA string `json:"sha"`
	}
	if err := g.do(ctx, http.MethodPost, g.repoURL("/git/blobs"), in, &out); err != nil {
		return "", err
	}
	return out.SHA, nil
}

func (g *githubClient) FindOpenPR(ctx context.Context, headBranch string) (string, bool, error) {
	// head filter is owner:branch.
	url := g.repoURL("/pulls?state=open&head=" + g.owner + ":" + headBranch)
	var prs []struct {
		HTMLURL string `json:"html_url"`
	}
	if err := g.do(ctx, http.MethodGet, url, nil, &prs); err != nil {
		return "", false, err
	}
	if len(prs) == 0 {
		return "", false, nil
	}
	return prs[0].HTMLURL, true, nil
}

func (g *githubClient) OpenPR(ctx context.Context, head, base, title, body string) (string, error) {
	in := map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if err := g.do(ctx, http.MethodPost, g.repoURL("/pulls"), in, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}
