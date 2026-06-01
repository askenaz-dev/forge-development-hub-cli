package portalapi

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// EventSink consumes normalized events. Implementations must be cheap and
// non-blocking; anything slow (network export) belongs behind the async
// Emitter buffer, never inline.
type EventSink interface {
	Consume(Event)
}

// Emitter is the ingestion fan-out. Emit must never block the caller or
// surface an error: a slow or unavailable downstream degrades to buffering
// and then dropping, never to added request latency or a failed request.
type Emitter interface {
	Emit(Event)
	Shutdown(ctx context.Context) error
}

// asyncEmitter buffers events in a bounded channel and fans them out to its
// sinks on a single background goroutine. When the buffer is full it drops
// the event (incrementing a counter) rather than blocking the producer.
type asyncEmitter struct {
	ch      chan Event
	sinks   []EventSink
	logger  *slog.Logger
	wg      sync.WaitGroup
	dropped uint64
	mu      sync.Mutex
	otlp    func(context.Context) error // OTLP provider shutdown, nil when disabled
}

const emitterBuffer = 4096

func newAsyncEmitter(logger *slog.Logger, sinks ...EventSink) *asyncEmitter {
	e := &asyncEmitter{
		ch:     make(chan Event, emitterBuffer),
		sinks:  sinks,
		logger: logger,
	}
	e.wg.Add(1)
	go e.run()
	return e
}

func (e *asyncEmitter) run() {
	defer e.wg.Done()
	for ev := range e.ch {
		for _, s := range e.sinks {
			s.Consume(ev)
		}
	}
}

// Emit enqueues an event without blocking. If the buffer is full the event is
// dropped and counted; ingestion must never stall the request that produced
// the event.
func (e *asyncEmitter) Emit(ev Event) {
	select {
	case e.ch <- ev:
	default:
		e.mu.Lock()
		e.dropped++
		dropped := e.dropped
		e.mu.Unlock()
		// Log sparingly: only on the first drop and every 1000th after, so a
		// flood does not become its own noise source.
		if dropped == 1 || dropped%1000 == 0 {
			e.logger.Warn("telemetry buffer full; dropping events", "dropped_total", dropped)
		}
	}
}

// Shutdown drains the buffer and shuts the OTLP provider down, honoring the
// context deadline.
func (e *asyncEmitter) Shutdown(ctx context.Context) error {
	close(e.ch)
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if e.otlp != nil {
		return e.otlp(ctx)
	}
	return nil
}

// slogSink writes every event as a single structured log line. This is the
// always-on durable record (the comment in activation.go names structured
// logs as the durable record); the OTLP sink and in-memory store are
// additive.
type slogSink struct{ logger *slog.Logger }

func (s slogSink) Consume(ev Event) {
	attrs := []any{
		"event", "telemetry",
		"event_name", ev.EventName,
		"tier", tierOf(ev.EventName),
		"occurred_at", ev.OccurredAt.Format(time.RFC3339),
	}
	if ev.InstallID != "" {
		attrs = append(attrs, "install_id", ev.InstallID)
	}
	if ev.WizardSessionID != "" {
		attrs = append(attrs, "wizard_session_id", ev.WizardSessionID)
	}
	for k, v := range ev.Attributes {
		attrs = append(attrs, "attr_"+k, v)
	}
	s.logger.Info("telemetry", attrs...)
}

// nopEmitter discards everything. Used when no logger is wired (defensive).
type nopEmitter struct{}

func (nopEmitter) Emit(Event)                      {}
func (nopEmitter) Shutdown(context.Context) error { return nil }
