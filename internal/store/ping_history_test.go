package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
	"vibemonitor/pkg/protocol"
)

func pingHistoryNode(t testing.TB, s *Store) *Node {
	t.Helper()
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "history", Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPingPreviewUsesRollingDayAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	n := pingHistoryNode(t, s)
	now := time.Now().UTC()
	start := now.Add(-59 * time.Minute)
	s.mu.Lock()
	s.nodes[n.UUID].PingObserved["route"] = PingObservation{Host: "example.com:443", StartedAt: start.Unix()}
	s.mu.Unlock()
	for i := 0; i < 60; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		r := perfReport(at)
		latency := 12
		if i < 36 {
			latency = -1
		}
		r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: latency}}
		if _, err = s.ingestReportAt(n.Token, r, at); err != nil {
			t.Fatal(err)
		}
		flushTestHistory(t, s)
	}
	check := func() {
		t.Helper()
		preview := s.GetDashboardNodes()[0].PingPreview[0]
		if preview.Loss == nil || *preview.Loss != 60 {
			t.Fatalf("rolling-day loss = %v, want 60%%", preview.Loss)
		}
		if len(preview.Samples) != 24 {
			t.Fatalf("preview chart has %d points", len(preview.Samples))
		}
		for _, sample := range preview.Samples {
			if sample.Latency < 0 {
				t.Fatal("chart must contain only latest 24 successful samples")
			}
		}
	}
	check()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check()
}

func TestPingLossWindowExpiresWithoutReports(t *testing.T) {
	w := &pingLossWindow{}
	w.append(PingSample{Timestamp: 1, Latency: -1})
	w.append(PingSample{Timestamp: 61, Latency: 1})
	if lost, total := w.counts(61); lost != 0 || total != 1 {
		t.Fatalf("window counts %d/%d", lost, total)
	}
	if lost, total := w.counts(62); lost != 0 || total != 0 {
		t.Fatalf("expired window counts %d/%d", lost, total)
	}
	for i := 2; i < 2000; i++ {
		w.append(PingSample{Timestamp: int64(i * 60), Latency: 1})
	}
	if len(w.points) > 1441 {
		t.Fatalf("unbounded rolling memory: %d", len(w.points))
	}
}

func TestPingObservationStartsAtNewHostAndMethod(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	s.mu.Lock()
	s.nodes[n.UUID].CreatedAt = time.Now().Add(-400 * 24 * time.Hour)
	s.mu.Unlock()
	if err := s.UpdateNodeWithOptions(n.UUID, NodeOptions{Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "new.example.com:443"}}}, ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := perfReport(now)
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "new.example.com:443", Method: "tcp", Latency: 8}}
	if _, err := s.ingestReportAt(n.Token, r, now); err != nil {
		t.Fatal(err)
	}
	flushTestHistory(t, s)
	response, err := s.GetPingHistory(n.UUID, "route", "all")
	if err != nil {
		t.Fatal(err)
	}
	if response.Stats.PacketLoss != 0 || response.Stats.TotalCount != 1 || len(response.OfflineIntervals) != 0 || response.StartTime < now.Unix()-2 {
		t.Fatalf("new target falsely offline: %+v", response)
	}
	// Changing measurement method starts a fresh series, even under the same name/host.
	at := now.Add(time.Second)
	r.UpdatedAt = at
	r.PingResults[0].Method = "icmp"
	if _, err = s.ingestReportAt(n.Token, r, at); err != nil {
		t.Fatal(err)
	}
	flushTestHistory(t, s)
	response, err = s.GetPingHistory(n.UUID, "route", "24h")
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "icmp" || response.Stats.PacketLoss != 0 || len(response.OfflineIntervals) != 0 {
		t.Fatalf("method mixed old history: %+v", response)
	}
}

func TestUnobservedOfflineTargetIsUnknown(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	s.mu.Lock()
	s.nodes[n.UUID].CreatedAt = time.Now().Add(-100 * 24 * time.Hour)
	s.nodes[n.UUID].LastSeen = time.Now().Add(-time.Hour)
	s.nodes[n.UUID].Online = false
	s.mu.Unlock()
	r, err := s.GetPingHistory(n.UUID, "route", "all")
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats.TotalCount != 0 || r.Stats.Current != -1 || len(r.OfflineIntervals) != 0 {
		t.Fatalf("unknown target counted as offline: %+v", r)
	}
}

func TestHistoryRequestsHonorCancellationWhileWaitingForSQLite(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	c1, err := s.sdb.readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := s.sdb.readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	for _, read := range []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := s.GetPingHistoryContext(ctx, n.UUID, "route", "all")
			return err
		},
		func(ctx context.Context) error {
			_, err := s.GetResourceHistoryContext(ctx, n.UUID, "cpu", "24h")
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		started := time.Now()
		err := read(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
			t.Fatalf("reader did not cancel: %v", err)
		}
	}
}

func seedLongPingHistory(t testing.TB, s *Store, n *Node, days int) int64 {
	t.Helper()
	now := time.Now().Unix()
	start := now - int64(days*86400)
	s.mu.Lock()
	s.nodes[n.UUID].PingObserved["route"] = PingObservation{Host: "example.com:443", Method: "tcp", StartedAt: start}
	s.nodes[n.UUID].CreatedAt = time.Unix(start-86400*100, 0)
	s.mu.Unlock()
	_, err := s.sdb.db.Exec(`WITH RECURSIVE samples(timestamp,ordinal) AS (
		SELECT ?,0 UNION ALL SELECT timestamp+60,ordinal+1 FROM samples WHERE timestamp+60<=?
	) INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency)
	SELECT ?,'route','example.com:443','tcp',timestamp,CASE WHEN ordinal%10=0 THEN -1 ELSE 12 END FROM samples`, start, now, n.UUID)
	if err != nil {
		t.Fatal(err)
	}
	return start
}

func TestLongPingHistoryIsBoundedAndStatisticsAreExact(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	start := seedLongPingHistory(t, s, n, 91)
	r, err := s.GetPingHistory(n.UUID, "route", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Samples) > maxChartSamples || len(r.Samples) == 0 {
		t.Fatalf("chart has %d rows", len(r.Samples))
	}
	if r.StartTime < start+86400 || len(r.OfflineIntervals) != 0 {
		t.Fatalf("retention created artificial outages: %+v", r.OfflineIntervals)
	}
	if r.Stats.PacketLoss != 10 || r.Stats.Avg != 12 || r.Stats.Min != 12 || r.Stats.Max != 12 {
		t.Fatalf("SQL aggregation changed stats: %+v", r.Stats)
	}
	for _, sample := range r.Samples {
		if sample.Latency != -1 {
			t.Fatal("aggregated bucket hid packet loss")
		}
	}
}

func BenchmarkPingHistory90Days(b *testing.B) {
	s, err := New(filepath.Join(b.TempDir(), "data.db"), "password")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	n := pingHistoryNode(b, s)
	start := seedLongPingHistory(b, s, n, 90)
	b.Run("legacy_raw", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			samples, err := s.sdb.getPingHistory(n.UUID, "route", "example.com:443", "tcp", start)
			if err != nil {
				b.Fatal(err)
			}
			legacyDownsamplePingSamples(samples, maxChartSamples)
		}
	})
	b.Run("stream_aggregate_cold", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			response := &PingHistoryResponse{UUID: n.UUID, Target: "route", Host: "example.com:443", Method: "tcp", StartTime: start, EndTime: time.Now().Unix()}
			if err := s.sdb.queryPingHistory(context.Background(), response, s.nodes[n.UUID].PingObserved["route"]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		if _, err := s.GetPingHistory(n.UUID, "route", "all"); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := s.GetPingHistory(n.UUID, "route", "all"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestPingSeriesEpochPersistsWithSamplesBeforeSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n := pingHistoryNode(t, s)
	now := time.Now().UTC()
	for i, entry := range []struct {
		method  string
		at      time.Time
		latency int
	}{{"tcp", now.Add(-10 * time.Minute), -1}, {"icmp", now.Add(-time.Minute), 8}, {"tcp", now, 8}} {
		r := perfReport(entry.at)
		r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: entry.method, Latency: entry.latency}}
		if _, err = s.ingestReportAt(n.Token, r, entry.at); err != nil {
			t.Fatal(err)
		}
		flushTestHistory(t, s)
		if i == 0 {
			if err = s.Save(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Open another store before Close/Save, matching a process crash after the
	// history commit. The last two method transitions are absent from node JSON.
	check := func(path string) {
		t.Helper()
		reopened, err := New(path, "password")
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		r, err := reopened.GetPingHistory(n.UUID, "route", "24h")
		if err != nil {
			t.Fatal(err)
		}
		if r.Method != "tcp" || r.StartTime != now.Unix() || r.Stats.TotalCount != 1 || r.Stats.PacketLoss != 0 {
			t.Fatalf("reboot resurrected old method series: %+v", r)
		}
	}
	check(path)
}

func TestPingHistoryCacheOwnsResultsAndInvalidatesNewSamples(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	now := time.Now().UTC()
	r := perfReport(now.Add(-time.Minute))
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 12}}
	if _, err := s.ingestReportAt(n.Token, r, r.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	flushTestHistory(t, s)
	first, err := s.GetPingHistory(n.UUID, "route", "1h")
	if err != nil {
		t.Fatal(err)
	}
	first.Host = ""
	first.Samples[0].Host = ""
	first.Samples[0].Latency = -1
	cached, err := s.GetPingHistory(n.UUID, "route", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if cached.Host == "" || cached.Samples[0].Host == "" || cached.Samples[0].Latency != 12 {
		t.Fatal("caller corrupted shared history cache")
	}
	r.UpdatedAt = now
	r.PingResults[0].Latency = 25
	if _, err = s.ingestReportAt(n.Token, r, now); err != nil {
		t.Fatal(err)
	}
	flushTestHistory(t, s)
	fresh, err := s.GetPingHistory(n.UUID, "route", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stats.TotalCount != 2 || fresh.Stats.Current != 25 {
		t.Fatalf("new sample did not invalidate cached stats: %+v", fresh.Stats)
	}
	if err = s.UpdateNodeWithOptions(n.UUID, NodeOptions{Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "other.example.com:443"}}}, ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.GetPingHistory(n.UUID, "route", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Host != "other.example.com:443" || changed.Stats.TotalCount != 0 || len(changed.Samples) != 0 {
		t.Fatal("target change reused old cached series")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.GetPingHistoryContext(ctx, n.UUID, "route", "1h"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cached request ignored cancellation: %v", err)
	}
}

func TestPingOfflineGapCrossingWindowBoundary(t *testing.T) {
	s := perfStore(t)
	n := pingHistoryNode(t, s)
	now := time.Now().Unix()
	start := now - 3600
	s.mu.Lock()
	s.nodes[n.UUID].PingObserved["route"] = PingObservation{Host: "example.com:443", Method: "tcp", StartedAt: start - 120, LastSampleAt: now}
	s.mu.Unlock()
	if err := s.sdb.recordPingSample(n.UUID, "route", "example.com:443", "tcp", start-120, 12); err != nil {
		t.Fatal(err)
	}
	for at := start + 120; at <= now; at += 60 {
		if err := s.sdb.recordPingSample(n.UUID, "route", "example.com:443", "tcp", at, 12); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.GetPingHistory(n.UUID, "route", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.OfflineIntervals) != 1 || r.OfflineIntervals[0].Start != r.StartTime || r.OfflineIntervals[0].End != start+120 || r.Stats.TotalCount != 61 || r.Stats.PacketLoss != 3.3 {
		t.Fatalf("clipped real outage was lost: %+v", r)
	}
}

func TestRetainedObservationSurvivesPruningAllRawHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n := pingHistoryNode(t, s)
	now := time.Now().UTC()
	old := now.Add(-95 * 24 * time.Hour)
	r := perfReport(old)
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 12}}
	if _, err = s.ingestReportAt(n.Token, r, old); err != nil {
		t.Fatal(err)
	}
	flushTestHistory(t, s)
	s.mu.Lock()
	s.pruneExpiredPingPreviewsLocked(now.Unix() - 86400)
	s.mu.Unlock()
	if _, err = s.sdb.pruneOldPingHistory(now.Unix() - pingHistoryRetentionSec); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history, err := reopened.GetPingHistory(n.UUID, "route", "all")
	if err != nil {
		t.Fatal(err)
	}
	if history.Method != "tcp" || len(history.Samples) != 0 || len(history.OfflineIntervals) != 1 || history.Stats.PacketLoss != 100 || history.StartTime < now.Unix()-pingHistoryRetentionSec {
		t.Fatalf("pruning reset established observation to unknown: %+v", history)
	}
}

func legacyDownsamplePingSamples(samples []PingSample, maxPoints int) []PingSample {
	if len(samples) <= maxPoints || maxPoints <= 0 {
		return samples
	}

	result := make([]PingSample, 0, maxPoints)
	bucketSize := float64(len(samples)) / float64(maxPoints)

	for i := 0; i < maxPoints; i++ {
		startIdx := int(float64(i) * bucketSize)
		endIdx := int(float64(i+1) * bucketSize)
		if endIdx > len(samples) {
			endIdx = len(samples)
		}
		if startIdx >= endIdx {
			continue
		}

		bucket := samples[startIdx:endIdx]
		var lossSample *PingSample
		var maxLatencySample PingSample
		maxLatency := -1
		var sumLatency int64
		validCount := 0

		for j := range bucket {
			s := bucket[j]
			if s.Latency < 0 {
				if lossSample == nil {
					lossCopy := s
					lossSample = &lossCopy
				}
			} else {
				validCount++
				sumLatency += int64(s.Latency)
				if s.Latency > maxLatency {
					maxLatency = s.Latency
					maxLatencySample = s
				}
			}
		}

		if lossSample != nil {
			result = append(result, *lossSample)
		} else if validCount > 0 {
			avgLat := int(math.Round(float64(sumLatency) / float64(validCount)))
			rep := maxLatencySample
			if maxLatency <= avgLat+15 {
				rep.Latency = avgLat
			}
			result = append(result, rep)
		}
	}

	return result
}
