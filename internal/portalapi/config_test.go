package portalapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi"
)

func TestLoadConfig_DefaultsHubPath(t *testing.T) {
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", t.TempDir())
	t.Setenv("FDH_PORTAL_HUB_PATH", "")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "/srv/hub", cfg.HubPath)
}

func TestLoadConfig_OverridesHubPath(t *testing.T) {
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", t.TempDir())
	t.Setenv("FDH_PORTAL_HUB_PATH", "/mnt/custom-hub")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "/mnt/custom-hub", cfg.HubPath)
}

func TestLoadConfig_RegistryNotRequired(t *testing.T) {
	// The portal serves from HubPath; the legacy registry env vars are
	// optional and their absence must not fail LoadConfig.
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", "")
	t.Setenv("FDH_PORTAL_REGISTRY_URL", "")
	t.Setenv("FDH_PORTAL_HUB_PATH", "/srv/hub")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "/srv/hub", cfg.HubPath)
}

func TestLoadConfig_HubPathDoesNotGateStartup(t *testing.T) {
	// Even with a non-existent hub path, LoadConfig must succeed — the
	// wire handlers will respond 503 at request time. UI endpoints are
	// unaffected.
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", t.TempDir())
	t.Setenv("FDH_PORTAL_HUB_PATH", "/nonexistent/path/should/not/error")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "/nonexistent/path/should/not/error", cfg.HubPath)
}
