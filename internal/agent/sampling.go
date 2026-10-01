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
	if interval <= 0 || interval > time.Second {
		interval = time.Second
	}
	// Establish the counter baseline before publishing any rates.
	if _, err := collector.GetReport(); err != nil {
		log.Printf("[Agent] Error establishing metrics baseline: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report, err := collector.GetReport()
			if err != nil {
				log.Printf("[Agent] Error collecting metrics: %v", err)
				// Do not upload an older sample after a collection failure.
				select {
				case <-reports:
				default:
				}
				continue
			}
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
