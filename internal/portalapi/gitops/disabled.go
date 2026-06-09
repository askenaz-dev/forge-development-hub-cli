package gitops

import "context"

// disabledClient is the Client returned when the GitHub App env is absent. Every
// method returns ErrGitopsNotConfigured so the API boots and serves catalog/
// admin reads while the write surface stays dark (portal-runtime-resilience).
// Handlers detect this via Enabled()==false (or the typed error) and respond
// 503 gitops_not_configured — never a 500, never a panic.
type disabledClient struct{}

// Disabled returns a Client that is not wired to any GitHub App.
func Disabled() Client { return disabledClient{} }

func (disabledClient) Enabled() bool { return false }

func (disabledClient) DefaultBranch(context.Context) (string, error) {
	return "", ErrGitopsNotConfigured
}

func (disabledClient) DefaultBranchSHA(context.Context) (string, error) {
	return "", ErrGitopsNotConfigured
}

func (disabledClient) BranchExists(context.Context, string) (bool, error) {
	return false, ErrGitopsNotConfigured
}

func (disabledClient) CreateBranch(context.Context, string, string) error {
	return ErrGitopsNotConfigured
}

func (disabledClient) GetFile(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, ErrGitopsNotConfigured
}

func (disabledClient) CommitFiles(context.Context, string, string, []FileChange, string) (string, error) {
	return "", ErrGitopsNotConfigured
}

func (disabledClient) FindOpenPR(context.Context, string) (string, bool, error) {
	return "", false, ErrGitopsNotConfigured
}

func (disabledClient) OpenPR(context.Context, string, string, string, string) (string, error) {
	return "", ErrGitopsNotConfigured
}
