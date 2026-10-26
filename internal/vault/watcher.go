package vault

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
)

// Watch runs the exporter on a ticker until ctx is cancelled. An initial
// full export runs immediately so the user sees output right away. Each
// tick performs a full export (cheap for realistic sizes; incremental
// sync is handled inside ExportAll via last_exported_at).
//
// While the loop runs it publishes WatchHeartbeatFile on start and on every
// tick, and removes it again on clean shutdown. The heartbeat is
// informational, not a lock: a second watcher simply overwrites it and status
// reports the most recent writer.
//
// Interval <= 0 is a no-op — treat as "watch disabled".
func (e *Exporter) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		return fmt.Errorf("vault watch: create vault directory: %w", err)
	}
	pid := os.Getpid()
	started := time.Now().UTC()
	e.publishHeartbeat(pid, started)
	defer func() {
		if err := RemoveWatchHeartbeat(e.root, pid); err != nil {
			e.log.Warn("vault watch heartbeat removal failed", "err", err)
		}
	}()
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if _, err := e.ExportAll(gctx); err != nil {
			e.log.Warn("vault initial export failed", "err", err)
		}
		// A long export must not let a live watcher look stale.
		e.publishHeartbeat(pid, started)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gctx.Done():
				return nil
			case <-ticker.C:
				e.publishHeartbeat(pid, started)
				stats, err := e.ExportAll(gctx)
				// Beat again after the export so a long pass does not let a
				// live watcher look stale.
				e.publishHeartbeat(pid, started)
				if err != nil {
					e.log.Warn("vault export failed", "err", err)
					continue
				}
				e.log.Info("vault export",
					"observations", stats.Observations,
					"sessions", stats.Sessions,
					"skills", stats.Skills,
					"tags", stats.Tags)
			}
		}
	})
	return g.Wait()
}

// publishHeartbeat writes the liveness record, logging rather than failing
// the loop: a diagnostic artifact must not stop the export it describes.
func (e *Exporter) publishHeartbeat(pid int, started time.Time) {
	if err := WriteWatchHeartbeat(e.root, WatchHeartbeat{
		PID:         pid,
		StartedAt:   started,
		HeartbeatAt: time.Now().UTC(),
	}); err != nil {
		e.log.Warn("vault watch heartbeat failed", "err", err)
	}
}

// ParseInterval wraps time.ParseDuration with a friendlier error so the
// CLI can produce a clean message for bad [vault].watch_interval values.
func ParseInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("watch_interval %q: %w (use Go durations like 5m, 1h)", s, err)
	}
	return d, nil
}
