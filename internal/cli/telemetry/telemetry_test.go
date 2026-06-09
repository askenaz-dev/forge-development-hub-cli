package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// allowedEventFields is the FROZEN allow-list of JSON keys the telemetry wire
// event may carry. Any new field on Event must be added here deliberately —
// and a field that could re-identify a user (username/email/host/ip/path/
// file-content) must NEVER appear. This test is the privacy guardrail.
var allowedEventFields = map[string]bool{
	"event":        true,
	"kind":         true,
	"namespace":    true,
	"name":         true,
	"version":      true,
	"content_hash": true,
	"scope":        true,
	"registry":     true,
	"os":           true,
	"locale":       true,
	"install_id":   true,
	"timestamp":    true,
	"rating":       true,
	"category":     true,
	"text":         true,
}

// forbiddenSubstrings are tokens that must not appear in any telemetry field
// name — a defense-in-depth check against an accidental PII column.
var forbiddenSubstrings = []string{
	"user", "email", "host", "ip", "path", "file", "machine", "account",
	"username", "login", "address", "mac", "serial",
}

func TestEventSchemaHasNoPII(t *testing.T) {
	typ := reflect.TypeOf(Event{})
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("field %s has no json name", typ.Field(i).Name)
		}
		if !allowedEventFields[name] {
			t.Fatalf("Event has unexpected JSON field %q — telemetry payload is a closed allow-list; "+
				"adding fields (especially identifying ones) is forbidden without review", name)
		}
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(strings.ToLower(name), bad) {
				t.Fatalf("Event field %q contains forbidden PII token %q", name, bad)
			}
		}
	}
}

func TestEventMarshalsNoForbiddenKeys(t *testing.T) {
	// A fully-populated event must serialize only allow-listed keys.
	ev := Event{
		Event: "install", Kind: "skill", Namespace: "forge", Name: "design-system",
		Version: "1.2.3", ContentHash: "abc", Scope: "project", Registry: "git:hub",
		OS: "linux", Locale: "en", InstallID: "deadbeef", Timestamp: "2026-01-01T00:00:00Z",
		Rating: 5, Category: "idea", Text: "great",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if !allowedEventFields[k] {
			t.Fatalf("serialized event carries non-allow-listed key %q", k)
		}
	}
}

// fakeEnv builds a getenv stub from a map.
func fakeEnv(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestResolvePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		config     string
		env        map[string]string
		wantOn     bool
		wantSource EnablementSource
	}{
		{
			name:       "default is off",
			wantOn:     false,
			wantSource: SourceDefault,
		},
		{
			name:       "config true enables",
			config:     "true",
			wantOn:     true,
			wantSource: SourceConfig,
		},
		{
			name:       "config false disables",
			config:     "false",
			wantOn:     false,
			wantSource: SourceConfig,
		},
		{
			name:       "env FDH_TELEMETRY=1 enables",
			env:        map[string]string{"FDH_TELEMETRY": "1"},
			wantOn:     true,
			wantSource: SourceEnv,
		},
		{
			name:       "env outranks config: env=0 beats config true",
			config:     "true",
			env:        map[string]string{"FDH_TELEMETRY": "0"},
			wantOn:     false,
			wantSource: SourceEnv,
		},
		{
			name:       "DO_NOT_TRACK forces off over config true",
			config:     "true",
			env:        map[string]string{"DO_NOT_TRACK": "1"},
			wantOn:     false,
			wantSource: SourceDoNotTrack,
		},
		{
			name:       "DO_NOT_TRACK forces off over env opt-in",
			env:        map[string]string{"DO_NOT_TRACK": "1", "FDH_TELEMETRY": "1"},
			wantOn:     false,
			wantSource: SourceDoNotTrack,
		},
		{
			name:       "DO_NOT_TRACK=0 does not force off",
			config:     "true",
			env:        map[string]string{"DO_NOT_TRACK": "0"},
			wantOn:     true,
			wantSource: SourceConfig,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(tc.config, fakeEnv(tc.env))
			if d.Enabled != tc.wantOn || d.Source != tc.wantSource {
				t.Fatalf("Resolve(%q,%v) = {%v,%s}; want {%v,%s}",
					tc.config, tc.env, d.Enabled, d.Source, tc.wantOn, tc.wantSource)
			}
		})
	}
}

func TestResolveWithConsent(t *testing.T) {
	dir := t.TempDir()

	// No consent recorded → default OFF.
	m := NewManager(dir)
	if d := m.ResolveWithConsent("", fakeEnv(nil)); d.Enabled || d.Source != SourceDefault {
		t.Fatalf("fresh manager should be default-off, got {%v,%s}", d.Enabled, d.Source)
	}

	// Record affirmative consent → enabled via consent source.
	if err := m.RecordConsent(true); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(dir)
	if d := m2.ResolveWithConsent("", fakeEnv(nil)); !d.Enabled || d.Source != SourceConsent {
		t.Fatalf("after consent grant want {true,consent}, got {%v,%s}", d.Enabled, d.Source)
	}

	// DO_NOT_TRACK still forces off even with consent granted.
	if d := m2.ResolveWithConsent("", fakeEnv(map[string]string{"DO_NOT_TRACK": "1"})); d.Enabled {
		t.Fatalf("DO_NOT_TRACK must override consent grant")
	}

	// Config/env decision outranks consent (never reaches consent branch).
	if d := m2.ResolveWithConsent("false", fakeEnv(nil)); d.Enabled || d.Source != SourceConfig {
		t.Fatalf("config false must outrank consent grant, got {%v,%s}", d.Enabled, d.Source)
	}
}

func TestInstallIDIsRotatableAndUncorrelatable(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id1, err := m.InstallID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id1) != 64 {
		t.Fatalf("install id must be 64 hex chars, got %d (%q)", len(id1), id1)
	}
	// Stable across calls until rotated.
	id1b, _ := m.InstallID()
	if id1 != id1b {
		t.Fatalf("install id changed without rotation: %s != %s", id1, id1b)
	}
	// Rotation yields a different, uncorrelatable id.
	if err := m.Rotate(); err != nil {
		t.Fatal(err)
	}
	id2, _ := m.InstallID()
	if id2 == id1 {
		t.Fatalf("rotation did not change the install id")
	}
	// A fresh manager over the same dir reads the rotated salt (persisted).
	m3 := NewManager(dir)
	id3, _ := m3.InstallID()
	if id3 != id2 {
		t.Fatalf("rotated id not persisted: %s != %s", id3, id2)
	}
}

func TestInstallIDAutoRotates(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id1, _ := m.InstallID()
	// Back-date the salt beyond the auto-rotate window.
	m.load()
	m.st.SaltRotatedAt = time.Now().Add(-2 * autoRotateAfter)
	if err := m.save(); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(dir)
	id2, _ := m2.InstallID()
	if id2 == id1 {
		t.Fatalf("expected auto-rotation after the window elapsed")
	}
}

func TestConsentPromptIsOneTimeAndDefaultsDecline(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Empty input (just Enter) → decline; prompt is recorded as asked.
	granted, asked := m.MaybePrompt(true, false, strings.NewReader("\n"), io.Discard)
	if granted || !asked {
		t.Fatalf("empty answer should decline but mark asked; got granted=%v asked=%v", granted, asked)
	}
	if !m.ConsentAnswered() {
		t.Fatalf("consent should be recorded as answered")
	}

	// Second invocation must NOT prompt again (one-time).
	out := &bytes.Buffer{}
	g2, asked2 := m.MaybePrompt(true, false, strings.NewReader("y\n"), out)
	if asked2 {
		t.Fatalf("consent prompt must be one-time, but prompted again")
	}
	if g2 { // still the recorded decline
		t.Fatalf("second call should return the recorded decision (decline)")
	}
	if out.Len() != 0 {
		t.Fatalf("no prompt text should be written on the second call, got %q", out.String())
	}
}

func TestConsentPromptNeverUnderNonInteractive(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	out := &bytes.Buffer{}
	// interactive=false → never prompt, stay declined, nothing written.
	granted, asked := m.MaybePrompt(false, false, strings.NewReader("y\n"), out)
	if granted || asked {
		t.Fatalf("non-interactive must not prompt; got granted=%v asked=%v", granted, asked)
	}
	if out.Len() != 0 {
		t.Fatalf("non-interactive prompt wrote text: %q", out.String())
	}
	if m.ConsentAnswered() {
		t.Fatalf("non-interactive run must not record a consent answer")
	}
}

func TestConsentPromptAffirmative(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	out := &bytes.Buffer{}
	granted, asked := m.MaybePrompt(true, false, strings.NewReader("y\n"), out)
	if !granted || !asked {
		t.Fatalf("'y' should grant; got granted=%v asked=%v", granted, asked)
	}
	if !strings.Contains(out.String(), PrivacyPolicyURL) {
		t.Fatalf("consent prompt must link the privacy policy URL")
	}
}

func TestConsentSkippedWhenAlreadyDecided(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	out := &bytes.Buffer{}
	granted, asked := m.MaybePrompt(true, true /*alreadyDecided*/, strings.NewReader("y\n"), out)
	if granted || asked {
		t.Fatalf("must not prompt when already decided; got granted=%v asked=%v", granted, asked)
	}
	if out.Len() != 0 {
		t.Fatalf("no prompt should be written when already decided")
	}
}

// TestDisabledEmitterMakesNoNetworkCall asserts the core opt-out guarantee:
// a disabled emitter never POSTs, even when given events.
func TestDisabledEmitterMakesNoNetworkCall(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	em := NewEmitter(srv.URL, false) // DISABLED
	em.Enqueue(Event{Event: "install", Timestamp: "now"})
	wait := em.FlushAsync(context.Background())
	wait()

	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("disabled emitter made %d network call(s); want 0", hits)
	}
}

// TestEnabledEmitterPostsAndBodyHasNoPII checks the happy path and re-asserts
// the no-PII invariant on the actual transmitted bytes.
func TestEnabledEmitterPostsAndBodyHasNoPII(t *testing.T) {
	bodies := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- b
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	em := NewEmitter(srv.URL, true)
	em.Enqueue(Event{
		Event: "install", Kind: "skill", Namespace: "forge", Name: "x",
		Version: "1.0.0", ContentHash: "h", Scope: "project", Registry: "git:hub",
		OS: CoarseOS(), Locale: "en", InstallID: "id", Timestamp: "2026-01-01T00:00:00Z",
	})
	wait := em.FlushAsync(context.Background())
	wait()

	select {
	case b := <-bodies:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("bad body: %v", err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
			if !allowedEventFields[k] {
				t.Fatalf("transmitted body carried non-allow-listed key %q", k)
			}
		}
		sort.Strings(keys)
	case <-time.After(3 * time.Second):
		t.Fatal("enabled emitter did not POST within the time box")
	}
}

// TestEmitterSwallowsUnreachableEndpoint asserts an unreachable endpoint never
// surfaces an error and the flush returns within the time box.
func TestEmitterSwallowsUnreachableEndpoint(t *testing.T) {
	// Use a closed server's address so dialing fails fast.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // now unreachable

	em := NewEmitter(addr, true)
	em.Enqueue(Event{Event: "install", Timestamp: "now"})
	done := make(chan struct{})
	go func() {
		wait := em.FlushAsync(context.Background())
		wait()
		close(done)
	}()
	select {
	case <-done:
		// success: flush completed (errors swallowed)
	case <-time.After(5 * time.Second):
		t.Fatal("flush against an unreachable endpoint did not return within the time box")
	}
}

func TestCoarseOSIsAllowListed(t *testing.T) {
	got := CoarseOS()
	switch got {
	case "darwin", "linux", "windows", "other":
		// ok — coarse buckets only, no arch/kernel/version detail
	default:
		t.Fatalf("CoarseOS returned non-coarse value %q", got)
	}
}
