package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// redirectConfigDir points os.UserConfigDir() at an isolated temp dir for the
// duration of the test, cross-platform (AppData on Windows, XDG on others).
func redirectConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", dir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

// newTestCmd returns a bare cobra command with stdio buffers wired so the
// telemetry session has an InOrStdin/ErrOrStderr to use.
func newTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "test"}
	c.SetIn(bytes.NewReader(nil))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetContext(context.Background())
	return c
}

// countingIngest stands in for the portal ingest endpoint and counts hits.
func countingIngest(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
}

// resetTelemetryViper isolates viper state for a test and points the endpoint
// at the given URL.
func resetTelemetryViper(t *testing.T, endpoint, enabled string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	if endpoint != "" {
		viper.Set("telemetry.endpoint", endpoint)
	}
	if enabled != "" {
		viper.Set("telemetry.enabled", enabled)
	}
}

// TestEmitDefaultOffSendsNothing: with no opt-in anywhere, an install-shaped
// emit must produce zero network calls.
func TestEmitDefaultOffSendsNothing(t *testing.T) {
	var hits int32
	srv := countingIngest(t, &hits)
	defer srv.Close()
	resetTelemetryViper(t, srv.URL, "") // default off

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("FDH_TELEMETRY", "")
	// Point config dir at a temp dir so no real state leaks in.
	redirectConfigDir(t)

	cmd := newTestCmd()
	ts := newTelemetrySession(cmd)
	if ts.enabled {
		t.Fatalf("telemetry must be OFF by default")
	}
	ts.emit("install", "skill", "forge", "x", "1.0.0", "h", "project", "git:hub")
	ts.flush(context.Background())

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("default-off emit sent %d request(s); want 0", got)
	}
}

// TestEmitDoNotTrackSendsNothing: even with config opt-in, DO_NOT_TRACK forces
// zero emission.
func TestEmitDoNotTrackSendsNothing(t *testing.T) {
	var hits int32
	srv := countingIngest(t, &hits)
	defer srv.Close()
	resetTelemetryViper(t, srv.URL, "true") // config opt-in

	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("FDH_TELEMETRY", "")
	redirectConfigDir(t)

	cmd := newTestCmd()
	ts := newTelemetrySession(cmd)
	if ts.enabled {
		t.Fatalf("DO_NOT_TRACK must force telemetry OFF even with config opt-in")
	}
	ts.emit("install", "skill", "forge", "x", "1.0.0", "h", "project", "git:hub")
	ts.flush(context.Background())

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("DO_NOT_TRACK emit sent %d request(s); want 0", got)
	}
}

// TestEmitEnabledSendsOne: with config opt-in and no DO_NOT_TRACK, a single
// install emit lands exactly one request.
func TestEmitEnabledSendsOne(t *testing.T) {
	var hits int32
	srv := countingIngest(t, &hits)
	defer srv.Close()
	resetTelemetryViper(t, srv.URL, "true")

	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("FDH_TELEMETRY", "")
	redirectConfigDir(t)

	cmd := newTestCmd()
	ts := newTelemetrySession(cmd)
	if !ts.enabled {
		t.Fatalf("config opt-in should enable telemetry")
	}
	ts.emit("install", "skill", "forge", "x", "1.0.0", "h", "project", "git:hub")
	ts.flush(context.Background())

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("enabled emit sent %d request(s); want 1", got)
	}
}
