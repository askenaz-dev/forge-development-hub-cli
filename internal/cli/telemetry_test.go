package cli

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestTelemetryEnabled_DoNotTrackWins(t *testing.T) {
	viper.Set("telemetry.enabled", true)
	defer viper.Set("telemetry.enabled", nil)
	t.Setenv("DO_NOT_TRACK", "1")
	if telemetryEnabled() {
		t.Fatal("DO_NOT_TRACK must disable telemetry even when config enables it")
	}
}

func TestTelemetryEnabled_OptOutViaConfig(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	viper.Set("telemetry.enabled", false)
	defer viper.Set("telemetry.enabled", nil)
	if telemetryEnabled() {
		t.Fatal("telemetry.enabled=false must disable telemetry")
	}
}

func TestTelemetryEndpoint_DerivedFromRegistryURL(t *testing.T) {
	viper.Set("telemetry.endpoint", "")
	viper.Set("registry.url", "https://hub.example.com/some/path")
	defer func() { viper.Set("registry.url", nil); viper.Set("telemetry.endpoint", nil) }()
	got := telemetryEndpoint()
	if got != "https://hub.example.com/api/v1/events" {
		t.Fatalf("derived endpoint wrong: %q", got)
	}
}

func TestTelemetryEndpoint_GitRegistryYieldsNone(t *testing.T) {
	viper.Set("telemetry.endpoint", "")
	viper.Set("registry.url", "git@github.com:org/hub.git")
	defer func() { viper.Set("registry.url", nil) }()
	if ep := telemetryEndpoint(); ep != "" {
		t.Fatalf("git transport must yield no events endpoint, got %q", ep)
	}
}

func TestLifecycleAttrs_OnlyEnumeratedKeys(t *testing.T) {
	allowed := map[string]bool{
		"kind": true, "namespace": true, "name": true, "version": true,
		"os": true, "cli_version": true, "scope": true, "agent": true,
	}
	attrs := lifecycleAttrs("skill", "dx", "owasp", "1.0.0", "user", []string{"claude", "copilot"}, BuildInfo{Version: "9.9"})
	for k := range attrs {
		if !allowed[k] {
			t.Fatalf("lifecycle attrs leaked a non-enumerated key: %q", k)
		}
	}
	if attrs["agent"] != "claude,copilot" {
		t.Fatalf("agents should be joined: %q", attrs["agent"])
	}
}

func TestClassifyErrorClass(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{Wrap(ExitRegistryUnreach, errString("boom")), "network"},
		{Wrap(ExitPermission, errString("denied")), "permission"},
		{Errorf(ExitGenericFailure, "signature verification failed"), "signature_mismatch"},
		{Errorf(ExitGenericFailure, "no space left on device"), "disk"},
		{Errorf(ExitGenericFailure, "something else"), "other"},
	}
	for _, c := range cases {
		if got := classifyErrorClass(c.err); got != c.want {
			t.Errorf("classifyErrorClass(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestEmitTelemetry_FailureSilentAndBounded(t *testing.T) {
	viper.Set("telemetry.enabled", true)
	viper.Set("telemetry.endpoint", "http://127.0.0.1:1/api/v1/events") // refused
	defer func() { viper.Set("telemetry.enabled", nil); viper.Set("telemetry.endpoint", nil) }()
	t.Setenv("DO_NOT_TRACK", "")

	cmd := &cobra.Command{}
	cmd.Flags().BoolP("verbose", "v", false, "")

	start := time.Now()
	// Must not panic and must not block beyond the bounded timeout.
	emitTelemetry(cmd, EventNameInstalled, map[string]string{"kind": "skill", "name": "x"})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("emit blocked too long: %v", elapsed)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
