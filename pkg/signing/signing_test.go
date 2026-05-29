package signing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/signing"
)

type stubVerifier struct {
	available bool
	signer    string
	err       error
}

func (s stubVerifier) Available() bool { return s.available }
func (s stubVerifier) Verify(_ context.Context, _, _ string) (string, error) {
	return s.signer, s.err
}

func TestCheck_AbsentSignature_DefaultInstalls(t *testing.T) {
	signer, err := signing.Check(context.Background(), signing.PolicyDefault, "abc", "", stubVerifier{available: true})
	require.NoError(t, err)
	require.Empty(t, signer)
}

func TestCheck_AbsentSignature_RequireAborts(t *testing.T) {
	_, err := signing.Check(context.Background(), signing.PolicyRequire, "abc", "", stubVerifier{available: true})
	require.Error(t, err)
}

func TestCheck_PresentSignature_CosignUnavailable_DefaultInstalls(t *testing.T) {
	signer, err := signing.Check(context.Background(), signing.PolicyDefault, "abc", "sig", stubVerifier{available: false})
	require.NoError(t, err)
	require.Empty(t, signer)
}

func TestCheck_PresentSignature_CosignUnavailable_RequireAborts(t *testing.T) {
	_, err := signing.Check(context.Background(), signing.PolicyRequire, "abc", "sig", stubVerifier{available: false})
	require.Error(t, err)
}

func TestCheck_VerifyFails_Aborts(t *testing.T) {
	_, err := signing.Check(context.Background(), signing.PolicyDefault, "abc", "sig",
		stubVerifier{available: true, err: errors.New("bad sig")})
	require.Error(t, err)
}

func TestCheck_VerifyOK_ReturnsSigner(t *testing.T) {
	signer, err := signing.Check(context.Background(), signing.PolicyRequire, "abc", "sig",
		stubVerifier{available: true, signer: "ci@forge"})
	require.NoError(t, err)
	require.Equal(t, "ci@forge", signer)
}

func TestPolicyFromEnv(t *testing.T) {
	t.Setenv("FDH_REQUIRE_SIGNATURES", "true")
	require.Equal(t, signing.PolicyRequire, signing.PolicyFromEnv())
	t.Setenv("FDH_REQUIRE_SIGNATURES", "")
	require.Equal(t, signing.PolicyDefault, signing.PolicyFromEnv())
}
