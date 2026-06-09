package portalapi

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// RunTelemetryLoop runs the in-process aggregation + retention pass on an
// interval, mirroring RunRefreshLoop (design D6). It is started from main()
// alongside the refresh loop. The loop:
//
//   - PAUSES when the store is unavailable (a degraded noop) — no error, no
//     crash; it resumes automatically if the store recovers and the process is
//     restarted (the store handle is fixed for the process lifetime).
//   - is PINNED to the lowest-ordinal replica so multiple StatefulSet replicas
//     do not double-write the upserted aggregates (design D6). Non-elected
//     replicas return immediately without scheduling any pass.
//
// Failures are logged and non-fatal; the next tick retries.
func (s *Server) RunTelemetryLoop(ctx context.Context) {
	if s.telemetry == nil || !s.telemetry.Available() {
		s.logger.Info("telemetry aggregation loop not started (store unavailable)")
		return
	}
	if !isLowestOrdinalReplica() {
		s.logger.Info("telemetry aggregation loop not started on this replica " +
			"(pinned to the lowest-ordinal replica to avoid double-writes)")
		return
	}

	interval := s.cfg.TelemetryAggregateInterval
	if interval <= 0 {
		interval = time.Hour
	}

	s.logger.Info("telemetry aggregation loop started",
		"interval", interval.String(),
		"retention", s.cfg.TelemetryRetention.String())

	// Run one pass promptly so a freshly-started leader rolls up without waiting
	// a full interval; then tick.
	if err := s.telemetry.Aggregate(ctx, s.cfg.TelemetryRetention); err != nil {
		s.logger.Warn("telemetry aggregation pass failed", "err", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.telemetry.Aggregate(ctx, s.cfg.TelemetryRetention); err != nil {
				s.logger.Warn("telemetry aggregation pass failed", "err", err)
			}
		}
	}
}

// isLowestOrdinalReplica reports whether THIS process is the elected aggregator
// (design D6). In a Kubernetes StatefulSet, the pod hostname ends in a stable
// ordinal suffix ("<name>-<ordinal>"), and the lowest ordinal is always "-0".
// Election rule:
//
//   - If POD_NAME (or hostname) ends in "-<n>" we elect iff n == 0.
//   - If the ordinal is undeterminable (no suffix, local/dev, single binary),
//     we ELECT — a single replica must run the loop. This makes single-replica
//     and local deployments safe by default; only a multi-replica StatefulSet
//     with proper ordinal hostnames de-elects the non-zero replicas.
//
// An explicit FDH_TELEMETRY_AGGREGATE override forces the decision for testing
// or unusual topologies: "1"/"true" elect, "0"/"false" de-elect.
func isLowestOrdinalReplica() bool {
	if v := strings.TrimSpace(os.Getenv("FDH_TELEMETRY_AGGREGATE")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}

	name := os.Getenv("POD_NAME")
	if name == "" {
		if h, err := os.Hostname(); err == nil {
			name = h
		}
	}
	ord, ok := ordinalSuffix(name)
	if !ok {
		// Undeterminable ordinal → single-replica/local: elect.
		return true
	}
	return ord == 0
}

// ordinalSuffix extracts the trailing "-<n>" ordinal from a StatefulSet pod
// name. Returns (n, true) when the name ends in a non-negative integer after a
// final hyphen; otherwise (0, false).
func ordinalSuffix(name string) (int, bool) {
	i := strings.LastIndex(name, "-")
	if i < 0 || i == len(name)-1 {
		return 0, false
	}
	n, err := strconv.Atoi(name[i+1:])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
