package portalapi

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestNormalizeEvent_RejectsUnknownName(t *testing.T) {
	ev := Event{EventName: "totally.made.up"}
	if normalizeEvent(&ev) {
		t.Fatal("expected unknown event name to be rejected")
	}
}

func TestNormalizeEvent_DropsUnknownKeysKeepsEvent(t *testing.T) {
	ev := Event{
		EventName:  EventComponentInstalled,
		Attributes: map[string]string{"kind": "skill", "name": "owasp", "secret_prompt": "rm -rf /"},
	}
	if !normalizeEvent(&ev) {
		t.Fatal("expected known event to be accepted")
	}
	if _, ok := ev.Attributes["secret_prompt"]; ok {
		t.Fatal("unknown attribute key must be dropped")
	}
	if ev.Attributes["kind"] != "skill" || ev.Attributes["name"] != "owasp" {
		t.Fatal("allowed attributes must be preserved")
	}
	if ev.OccurredAt.IsZero() {
		t.Fatal("OccurredAt must be stamped when omitted")
	}
}

func TestNormalizeEvent_NormalizesErrorClass(t *testing.T) {
	ev := Event{EventName: EventInstallFailed, Attributes: map[string]string{"error_class": "/home/user/secret/path"}}
	normalizeEvent(&ev)
	if ev.Attributes["error_class"] != "other" {
		t.Fatalf("invalid error_class must normalize to 'other', got %q", ev.Attributes["error_class"])
	}
}

func TestNormalizeEvent_ClampsFeedbackTextAndDropsBadSentiment(t *testing.T) {
	long := make([]byte, feedbackTextMax+500)
	for i := range long {
		long[i] = 'x'
	}
	ev := Event{EventName: EventFeedbackSubmitted, Attributes: map[string]string{
		"sentiment": "meh", "text": string(long),
	}}
	normalizeEvent(&ev)
	if _, ok := ev.Attributes["sentiment"]; ok {
		t.Fatal("invalid sentiment must be dropped")
	}
	if len(ev.Attributes["text"]) != feedbackTextMax {
		t.Fatalf("feedback text must be clamped to %d, got %d", feedbackTextMax, len(ev.Attributes["text"]))
	}
}

func TestTransformIP(t *testing.T) {
	cases := []struct {
		handling string
		in       string
		want     string
	}{
		{IPFull, "203.0.113.7:5555", "203.0.113.7:5555"},
		{IPDrop, "203.0.113.7:5555", ""},
		{IPTruncate, "203.0.113.7:5555", "203.0.113.0"},
		{IPTruncate, "[2001:db8::1]:443", "2001:db8::"},
	}
	for _, c := range cases {
		tc := TelemetryConfig{IPHandling: c.handling, ipHashSalt: "s"}
		if got := tc.transformIP(c.in); got != c.want {
			t.Errorf("transformIP(%q) handling=%s = %q, want %q", c.in, c.handling, got, c.want)
		}
	}
	// Hash is non-empty, stable, and not the raw IP.
	tc := TelemetryConfig{IPHandling: IPHash, ipHashSalt: "s"}
	h1 := tc.transformIP("203.0.113.7:1")
	h2 := tc.transformIP("203.0.113.7:2")
	if h1 == "" || h1 != h2 || h1 == "203.0.113.7" {
		t.Errorf("hash IP should be stable, non-empty, and not the raw IP: %q vs %q", h1, h2)
	}
}

func TestEventStore_RetentionPrunes(t *testing.T) {
	st := newEventStore(0, 24*time.Hour)
	now := time.Now().UTC()
	st.now = func() time.Time { return now }

	old := Event{EventName: EventBundleDownloaded, OccurredAt: now.Add(-48 * time.Hour),
		Attributes: map[string]string{"kind": "skill", "namespace": "dx", "name": "old"}}
	fresh := Event{EventName: EventBundleDownloaded, OccurredAt: now.Add(-1 * time.Hour),
		Attributes: map[string]string{"kind": "skill", "namespace": "dx", "name": "fresh"}}
	st.Consume(old)
	st.Consume(fresh)

	sum := st.Insights()
	if sum.Total != 1 {
		t.Fatalf("expired record must be pruned; total=%d", sum.Total)
	}
	if sum.Downloads["skill/dx/fresh"] != 1 || sum.Downloads["skill/dx/old"] != 0 {
		t.Fatalf("only the fresh download should survive: %+v", sum.Downloads)
	}
}

func TestEventStore_InsightsAggregates(t *testing.T) {
	st := newEventStore(0, 0) // unbounded retention
	st.Consume(Event{EventName: EventInstallFailed, OccurredAt: time.Now(), Attributes: map[string]string{"error_class": "network"}})
	st.Consume(Event{EventName: EventFeedbackSubmitted, OccurredAt: time.Now(), Attributes: map[string]string{"sentiment": "up"}})
	st.Consume(Event{EventName: EventSearchZero, OccurredAt: time.Now(), Attributes: map[string]string{"query": "kubernetes"}})
	sum := st.Insights()
	if sum.FailuresByClass["network"] != 1 {
		t.Errorf("expected one network failure: %+v", sum.FailuresByClass)
	}
	if sum.Feedback["up"] != 1 {
		t.Errorf("expected one up feedback: %+v", sum.Feedback)
	}
	if sum.ZeroResultQ["kubernetes"] != 1 {
		t.Errorf("expected demand gap for kubernetes: %+v", sum.ZeroResultQ)
	}
}

// captureSink records consumed events for assertions.
type captureSink struct {
	mu  sync.Mutex
	got []Event
}

func (c *captureSink) Consume(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, ev)
}

func (c *captureSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func TestAsyncEmitter_FlushesOnShutdown(t *testing.T) {
	cap := &captureSink{}
	em := newAsyncEmitter(slog.Default(), cap)
	for i := 0; i < 100; i++ {
		em.Emit(Event{EventName: EventBundleDownloaded, OccurredAt: time.Now()})
	}
	if err := em.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if cap.count() != 100 {
		t.Fatalf("expected 100 events flushed, got %d", cap.count())
	}
}

// blockingSink blocks on the first event until released, so the emitter's
// single worker is stuck and the buffer fills.
type blockingSink struct{ release chan struct{} }

func (b blockingSink) Consume(Event) { <-b.release }

func TestAsyncEmitter_DropsWhenFull(t *testing.T) {
	bs := blockingSink{release: make(chan struct{})}
	em := newAsyncEmitter(slog.Default(), bs)
	// Emit far more than the buffer can hold while the worker is blocked.
	for i := 0; i < emitterBuffer+200; i++ {
		em.Emit(Event{EventName: EventBundleDownloaded, OccurredAt: time.Now()})
	}
	em.mu.Lock()
	dropped := em.dropped
	em.mu.Unlock()
	if dropped == 0 {
		t.Fatal("expected events to be dropped when the buffer is full (non-blocking)")
	}
	close(bs.release) // unblock the worker so Shutdown can drain
	_ = em.Shutdown(context.Background())
}

func TestLoadTelemetryConfig_ModeDefaults(t *testing.T) {
	t.Setenv("FDH_TELEMETRY_MODE", "public")
	pub, err := loadTelemetryConfig()
	if err != nil {
		t.Fatal(err)
	}
	if pub.IPHandling != IPTruncate || !pub.AnonymousFirst() {
		t.Fatalf("public defaults wrong: %+v", pub)
	}

	t.Setenv("FDH_TELEMETRY_MODE", "internal")
	internal, err := loadTelemetryConfig()
	if err != nil {
		t.Fatal(err)
	}
	if internal.IPHandling != IPFull || internal.Identity != IdentityAttributed {
		t.Fatalf("internal defaults wrong: %+v", internal)
	}

	// Explicit override wins over the mode default.
	t.Setenv("FDH_TELEMETRY_MODE", "public")
	t.Setenv("FDH_TELEMETRY_IP_HANDLING", "drop")
	ovr, err := loadTelemetryConfig()
	if err != nil {
		t.Fatal(err)
	}
	if ovr.IPHandling != IPDrop {
		t.Fatalf("explicit override should win: %+v", ovr)
	}
}
