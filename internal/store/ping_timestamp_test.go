package store

import (
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestPingHistoryUsesMeasurementTimeAndSkipsCachedResults(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	at := time.Now().UTC().Truncate(time.Second)
	r := perfReport(at)
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 12, MeasuredAt: at.Unix()}}
	for _, offset := range []time.Duration{10*time.Second, 70*time.Second, 130*time.Second} {
		r.UpdatedAt = at.Add(offset)
		if _, err := s.ingestReportAt(n.Token, r, at.Add(offset)); err != nil { t.Fatal(err) }
	}
	flushTestHistory(t, s)
	var count int
	var first int64
	if err := s.sdb.db.QueryRow("SELECT COUNT(*), MIN(timestamp) FROM ping_history WHERE node_uuid = ?", n.UUID).Scan(&count, &first); err != nil { t.Fatal(err) }
	if count != 1 || first != at.Unix() { t.Fatalf("cached samples: count=%d timestamp=%d", count, first) }
	// A genuinely new probe advances the history clock, even if received later.
	r.PingResults[0].MeasuredAt = at.Add(150*time.Second).Unix()
	r.UpdatedAt = at.Add(160*time.Second)
	if _, err := s.ingestReportAt(n.Token, r, at.Add(160*time.Second)); err != nil { t.Fatal(err) }
	flushTestHistory(t, s)
	if err := s.sdb.db.QueryRow("SELECT COUNT(*) FROM ping_history WHERE node_uuid = ?", n.UUID).Scan(&count); err != nil { t.Fatal(err) }
	if count != 2 { t.Fatalf("new measurement count=%d", count) }
}
