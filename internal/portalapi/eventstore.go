package portalapi

import (
	"sort"
	"sync"
	"time"
)

// eventStore is a bounded, time-pruned in-memory store of recent events. It
// is the portal's own queryable record behind the admin insights view, and it
// is where the retention policy is enforced in-process: events older than the
// retention window are pruned and never returned. The durable analytics record
// lives downstream (OTLP → collector → store); this store exists so insights
// work without a round-trip to that backend during the pilot.
//
// It implements EventSink so it can sit on the emitter fan-out.
type eventStore struct {
	mu        sync.Mutex
	events    []Event
	maxEvents int
	retention time.Duration
	now       func() time.Time
}

func newEventStore(maxEvents int, retention time.Duration) *eventStore {
	if maxEvents <= 0 {
		maxEvents = 50000
	}
	return &eventStore{
		maxEvents: maxEvents,
		retention: retention,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *eventStore) Consume(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	s.pruneLocked()
}

// pruneLocked drops events past the retention window and clamps the slice to
// maxEvents. Caller holds the lock.
func (s *eventStore) pruneLocked() {
	if s.retention > 0 {
		cutoff := s.now().Add(-s.retention)
		i := 0
		for i < len(s.events) && s.events[i].OccurredAt.Before(cutoff) {
			i++
		}
		if i > 0 {
			s.events = append([]Event(nil), s.events[i:]...)
		}
	}
	if len(s.events) > s.maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-s.maxEvents:]...)
	}
}

// Insights aggregates the retained events into the admin summary. It prunes
// first so expired records never appear.
func (s *eventStore) Insights() InsightsSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()

	sum := InsightsSummary{
		EventCounts:    map[string]int{},
		Downloads:      map[string]int{},
		ZeroResultQ:    map[string]int{},
		NotFound:       map[string]int{},
		Installs:       map[string]int{},
		Uninstalls:     map[string]int{},
		FailuresByClass: map[string]int{},
		Feedback:       map[string]int{},
		Total:          len(s.events),
	}
	if len(s.events) > 0 {
		sum.WindowStart = s.events[0].OccurredAt
		sum.WindowEnd = s.events[len(s.events)-1].OccurredAt
	}
	for _, ev := range s.events {
		sum.EventCounts[ev.EventName]++
		coord := coordOf(ev)
		switch ev.EventName {
		case EventBundleDownloaded:
			sum.Downloads[coord]++
		case EventSearchZero:
			if q := ev.Attributes["query"]; q != "" {
				sum.ZeroResultQ[q]++
			}
		case EventComponentMissing:
			sum.NotFound[coord]++
		case EventComponentInstalled:
			sum.Installs[coord]++
		case EventComponentUninstalled:
			sum.Uninstalls[coord]++
		case EventInstallFailed:
			ec := ev.Attributes["error_class"]
			if ec == "" {
				ec = "other"
			}
			sum.FailuresByClass[ec]++
		case EventFeedbackSubmitted:
			sum.Feedback[ev.Attributes["sentiment"]]++
		}
	}
	return sum
}

// InsightsSummary is the admin insights wire shape: aggregates for downloads,
// demand gaps, churn, install failures, and feedback over the retained window.
type InsightsSummary struct {
	WindowStart     time.Time      `json:"window_start"`
	WindowEnd       time.Time      `json:"window_end"`
	Total           int            `json:"total"`
	EventCounts     map[string]int `json:"event_counts"`
	Downloads       map[string]int `json:"downloads"`
	ZeroResultQ     map[string]int `json:"zero_result_queries"`
	NotFound        map[string]int `json:"not_found"`
	Installs        map[string]int `json:"installs"`
	Uninstalls      map[string]int `json:"uninstalls"`
	FailuresByClass map[string]int `json:"install_failures_by_class"`
	Feedback        map[string]int `json:"feedback"`
}

func coordOf(ev Event) string {
	a := ev.Attributes
	kind, name := a["kind"], a["name"]
	ns := a["namespace"]
	if kind == "" && name == "" {
		return "(unknown)"
	}
	out := kind + "/" + ns + "/" + name
	return out
}

// topN reduces a count map to the N highest entries, for compact admin views.
// Exposed for handlers/tests that want a bounded projection.
func topN(m map[string]int, n int) []KV {
	kvs := make([]KV, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, KV{Key: k, Count: v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].Count != kvs[j].Count {
			return kvs[i].Count > kvs[j].Count
		}
		return kvs[i].Key < kvs[j].Key
	})
	if n > 0 && len(kvs) > n {
		kvs = kvs[:n]
	}
	return kvs
}

// KV is a key/count pair used by topN projections.
type KV struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}
