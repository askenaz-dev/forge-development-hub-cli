package gitops

import (
	"context"
	"fmt"
	"sync"
)

// fakeClient is an in-memory Client for unit tests. It records every call so
// tests can assert the EXACT sequence of branch/commit/PR primitives a composer
// invoked — and, crucially, that NO merge primitive exists (the interface has
// none; the fake records only the propose-only calls). It serves file content
// from an in-memory tree so composers can read+edit registry/harness bytes.
type fakeClient struct {
	mu sync.Mutex

	enabled       bool
	defaultBranch string
	headSHA       string

	// files maps "ref\x00path" → content; a "*" ref matches any.
	files map[string]string

	// existingOpenPR maps headBranch → url for FindOpenPR idempotency tests.
	existingOpenPR map[string]string

	// Recorded calls.
	createdBranches []branchCreate
	commits         []commitRecord
	openedPRs       []prRecord
	calls           []string // ordered method-name log
}

type branchCreate struct{ name, fromSHA string }

type commitRecord struct {
	branch  string
	baseSHA string
	files   []FileChange
	message string
}

type prRecord struct {
	head, base, title, body string
	url                     string
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		enabled:        true,
		defaultBranch:  "main",
		headSHA:        "basesha0000000000000000000000000000000000",
		files:          map[string]string{},
		existingOpenPR: map[string]string{},
	}
}

// setFile seeds file content visible at any ref.
func (f *fakeClient) setFile(path, content string) {
	f.files["*\x00"+path] = content
}

func (f *fakeClient) log(name string) { f.calls = append(f.calls, name) }

func (f *fakeClient) Enabled() bool { return f.enabled }

func (f *fakeClient) DefaultBranch(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("DefaultBranch")
	return f.defaultBranch, nil
}

func (f *fakeClient) DefaultBranchSHA(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("DefaultBranchSHA")
	return f.headSHA, nil
}

func (f *fakeClient) BranchExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("BranchExists")
	for _, b := range f.createdBranches {
		if b.name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeClient) CreateBranch(_ context.Context, name, fromSHA string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("CreateBranch")
	f.createdBranches = append(f.createdBranches, branchCreate{name, fromSHA})
	return nil
}

func (f *fakeClient) GetFile(_ context.Context, path, ref string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("GetFile")
	if v, ok := f.files[ref+"\x00"+path]; ok {
		return []byte(v), true, nil
	}
	if v, ok := f.files["*\x00"+path]; ok {
		return []byte(v), true, nil
	}
	return nil, false, nil
}

func (f *fakeClient) CommitFiles(_ context.Context, branch, baseSHA string, files []FileChange, message string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("CommitFiles")
	cp := make([]FileChange, len(files))
	copy(cp, files)
	f.commits = append(f.commits, commitRecord{branch, baseSHA, cp, message})
	// Reflect committed content back into the file store so subsequent reads see it.
	for _, fc := range files {
		if fc.Delete {
			delete(f.files, "*\x00"+fc.Path)
			continue
		}
		f.files["*\x00"+fc.Path] = string(fc.Content)
	}
	return fmt.Sprintf("commit-%d", len(f.commits)), nil
}

func (f *fakeClient) FindOpenPR(_ context.Context, headBranch string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("FindOpenPR")
	if url, ok := f.existingOpenPR[headBranch]; ok {
		return url, true, nil
	}
	return "", false, nil
}

func (f *fakeClient) OpenPR(_ context.Context, head, base, title, body string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("OpenPR")
	url := fmt.Sprintf("https://github.com/askenaz-dev/forge-development-hub/pull/%d", len(f.openedPRs)+1)
	f.openedPRs = append(f.openedPRs, prRecord{head, base, title, body, url})
	return url, nil
}

// lastCommitFile returns the content committed for path in the most recent
// commit, or ("", false).
func (f *fakeClient) lastCommitFile(path string) (string, bool) {
	if len(f.commits) == 0 {
		return "", false
	}
	for _, fc := range f.commits[len(f.commits)-1].files {
		if fc.Path == path {
			return string(fc.Content), true
		}
	}
	return "", false
}

// committedPaths returns the set of paths touched by the most recent commit.
func (f *fakeClient) committedPaths() []string {
	if len(f.commits) == 0 {
		return nil
	}
	var out []string
	for _, fc := range f.commits[len(f.commits)-1].files {
		out = append(out, fc.Path)
	}
	return out
}
