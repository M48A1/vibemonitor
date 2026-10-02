package agent

import (
	"context"
	"log"
	"time"

	"vibemonitor/pkg/monitor"
	"vibemonitor/pkg/protocol"
)

// collectReports measures at most one second of traffic per sample. The single
// pending report is replaced so a slow upload never builds a stale backlog.
func collectReports(ctx context.Context, collector monitor.Collector, interval time.Duration, reports chan protocol.Report) {
	if ctx.Err() != nil {
		return
	}
	if interval <= 0 || interval > time.Second {
		interval = time.Second
	}
	// Establish the counter baseline before publishing any rates.
	var failures repeatedErrorLog
	if _, err := collector.GetReport(); err != nil {
		if ctx.Err() != nil {
			return
		}
		failures.message(err, time.Now())
		log.Printf("[Agent] Error establishing metrics baseline: %v", err)
	}
	if ctx.Err() != nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			report, err := collector.GetReport()
			// A syscall may finish after shutdown. Keep any previously queued
			// valid sample intact instead of clearing it or publishing this one.
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if message, emit := failures.message(err, time.Now()); emit {
					log.Printf("[Agent] Error collecting metrics: %s", message)
				}
				// Do not upload an older sample after a collection failure.
				select {
				case <-reports:
				default:
				}
				continue
			}
			failures.reset()
			select {
			case <-reports:
			default:
			}
			select {
			case reports <- report:
			case <-ctx.Done():
				return
			}
		}
	}
}
