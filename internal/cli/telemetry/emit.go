package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// emitTimeout is the hard time box for the whole emission attempt. The
// command never blocks on telemetry beyond this; failures are swallowed.
const emitTimeout = 2 * time.Second

// maxQueued caps the in-memory event queue so a pathological loop cannot grow
// memory without bound. Excess events are dropped (best-effort).
const maxQueued = 64

// Emitter batches telemetry events and flushes them best-effort, async, and
// time-boxed. Construction is cheap and side-effect-free; nothing is sent
// unless Enabled is true AND Flush is called. The zero behavior (disabled)
// makes the whole subsystem a no-op.
//
// Usage pattern (short-lived CLI):
//
//	em := telemetry.NewEmitter(endpoint, enabled)
//	em.Enqueue(ev)            // cheap, in-memory
//	defer em.FlushAsync(ctx)  // best-effort, never blocks the command's result
//
// FlushAsync returns a wait function the caller MAY invoke with a short cap
// if it wants to give emission a chance before the process exits — but the
// command's exit code and output never depend on it.
type Emitter struct {
	endpoint string
	enabled  bool
	client   *http.Client

	mu     sync.Mutex
	queue  []Event
	closed bool
}

// NewEmitter builds an Emitter. When enabled is false the Emitter is a no-op:
// Enqueue discards, Flush does nothing, no network call is ever made.
func NewEmitter(endpoint string, enabled bool) *Emitter {
	return &Emitter{
		endpoint: endpoint,
		enabled:  enabled,
		client:   &http.Client{Timeout: emitTimeout},
	}
}

// Enqueue adds an event to the batch. It is a no-op when telemetry is
// disabled, when the queue is full, or after the Emitter is closed. It never
// errors and never blocks.
func (e *Emitter) Enqueue(ev Event) {
	if e == nil || !e.enabled {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || len(e.queue) >= maxQueued {
		return
	}
	e.queue = append(e.queue, ev)
}

// flushOnce drains the queue and POSTs each event to the ingest endpoint,
// strictly within ctx. ALL errors (network, non-2xx, marshaling) are
// swallowed: telemetry must never affect a command's outcome. Events are sent
// individually because the wire contract is one event per request body.
func (e *Emitter) flushOnce(ctx context.Context) {
	if e == nil || !e.enabled {
		return
	}
	e.mu.Lock()
	batch := e.queue
	e.queue = nil
	e.closed = true
	e.mu.Unlock()
	if len(batch) == 0 || e.endpoint == "" {
		return
	}
	for _, ev := range batch {
		if ctx.Err() != nil {
			return // time box elapsed — drop the rest, silently
		}
		e.postOne(ctx, ev)
	}
}

// postOne sends a single event, swallowing every error.
func (e *Emitter) postOne(ctx context.Context, ev Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return
	}
	// Drain+close so the connection can be reused; ignore the status code
	// entirely — a 4xx/5xx is not the command's problem.
	_ = resp.Body.Close()
}

// FlushAsync flushes the batch on a background goroutine bounded by the emit
// time box (and by the parent ctx, whichever is shorter). It returns
// immediately with a wait function: calling wait() blocks until emission
// finishes OR the time box elapses, whichever comes first. The command MAY
// call wait() right before exit to give in-flight events a chance to land,
// but MUST NOT make its success depend on the result.
func (e *Emitter) FlushAsync(parent context.Context) (wait func()) {
	if e == nil || !e.enabled {
		return func() {}
	}
	if parent == nil {
		parent = context.Background()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(parent, emitTimeout)
		defer cancel()
		e.flushOnce(ctx)
	}()
	return func() {
		// Bound the wait by the same time box so a slow ingest never delays
		// process exit beyond emitTimeout.
		t := time.NewTimer(emitTimeout)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
		}
	}
}
