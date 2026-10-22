package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestTrafficOptionValidation(t *testing.T) {
	for _, value := range []float64{-2, -0.5, math.NaN(), math.Inf(1), math.Inf(-1), 8589934592, 1e20} {
		for _, updating := range []bool{false, true} {
			for _, opts := range []NodeOptions{{TrafficLimitGB: value}, {InitialUsedGB: value}} {
				if err := validateTrafficOptions(opts, updating); !errors.Is(err, ErrInvalidNodeOptions) {
					t.Errorf("accepted %+v updating=%v: %v", opts, updating, err)
				}
			}
		}
	}
	for _, day := range []int{-2, 32} {
		if err := validateTrafficOptions(NodeOptions{ResetDay: day}, true); !errors.Is(err, ErrInvalidNodeOptions) {
			t.Errorf("accepted day %d", day)
		}
	}
	preserve := NodeOptions{ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1}
	if err := validateTrafficOptions(preserve, true); err != nil {
		t.Fatal(err)
	}
	if err := validateTrafficOptions(preserve, false); err == nil {
		t.Fatal("create accepts preserve sentinel")
	}
	for _, day := range []int{0, 1, 31} {
		if err := validateTrafficOptions(NodeOptions{ResetDay: day, TrafficLimitGB: 0.04, InitialUsedGB: 8589934591}, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResetDayChangeRecomputesMeasuredUsage(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, trafficLocation)
	for _, tc := range []struct {
		name           string
		oldDay, newDay int
		want           int64
	}{
		{"shorter", 1, 15, 100}, {"longer", 15, 1, 600}, {"previous-month", 1, 25, 650},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, _ := GetBillingCycleRange(tc.oldDay, now)
			manual := now.Add(-time.Hour)
			n := &Node{ResetDay: tc.oldDay, CycleStart: start, InitialUsed: 999, CurrentCycleUsed: 999, TrafficManualAt: &manual,
				DailyTraffic: map[string]int64{"2026-09-30": 50, "2026-10-10": 500, "2026-10-20": 100, "2026-10-23": 999}}
			s := &Store{}
			s.applyNodeOptionsLocked(n, NodeOptions{ResetDay: tc.newDay, TrafficLimitGB: -1, InitialUsedGB: -1}, 0, now)
			expectedStart, _ := GetBillingCycleRange(tc.newDay, now)
			if n.CurrentCycleUsed != tc.want || n.InitialUsed != 0 || n.TrafficManualAt != nil || !n.CycleStart.Equal(expectedStart) {
				t.Fatalf("incorrect recalculation: %+v", n)
			}
			if n.DailyTraffic["2026-10-10"] != 500 {
				t.Fatal("daily history changed")
			}
		})
	}
}

func TestResetDayPreservesSameCycleAndAppliesExplicitCorrection(t *testing.T) {
	now := time.Date(2026, 2, 28, 12, 0, 0, 0, trafficLocation)
	start, _ := GetBillingCycleRange(30, now)
	manual := now.Add(-time.Hour)
	n := &Node{ResetDay: 30, CycleStart: start, InitialUsed: 42, CurrentCycleUsed: 100, TrafficManualAt: &manual}
	s := &Store{}
	opts := NodeOptions{ResetDay: 31, TrafficLimitGB: -1, InitialUsedGB: -1}
	s.applyNodeOptionsLocked(n, opts, 0, now)
	if n.InitialUsed != 42 || n.CurrentCycleUsed != 100 || n.TrafficManualAt == nil {
		t.Fatal("same cycle lost manual correction")
	}
	opts.ResetDay = 15
	opts.InitialUsedGB = 1
	s.applyNodeOptionsLocked(n, opts, 0, now)
	if n.InitialUsed != 1<<30 {
		t.Fatal("explicit new offset lost")
	}
	used := 2.0
	opts.CycleUsedGB = &used
	s.applyNodeOptionsLocked(n, opts, 2<<30, now)
	if n.InitialUsed != 2<<30 || n.CurrentCycleUsed != 0 || n.TrafficManualAt == nil {
		t.Fatal("explicit total correction lost")
	}
}

func TestResetDayChangePreservesConcurrentTrafficAndPersists(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Only the metadata update under test may contend for the writer connection.
	close(s.stopFlush)
	<-s.flushDone
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "concurrent", ResetDay: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(trafficLocation)
	day := now.Day()
	if day == 1 {
		day = 15
	}
	today := now.Format("2006-01-02")
	s.mu.Lock()
	s.nodes[n.UUID].DailyTraffic = map[string]int64{today: 100}
	s.nodes[n.UUID].CurrentCycleUsed = 100
	s.nodes[n.UUID].InitialUsed = 999
	s.mu.Unlock()
	conn, err := s.sdb.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := s.sdb.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		done <- s.UpdateNodeWithOptions(n.UUID, NodeOptions{ResetDay: day, TrafficLimitGB: -1, InitialUsedGB: -1})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for s.sdb.db.Stats().WaitCount == before {
		if time.Now().After(deadline) {
			t.Fatal("write not reached")
		}
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	s.nodes[n.UUID].DailyTraffic[today] += 42
	s.nodes[n.UUID].CurrentCycleUsed += 42
	s.mu.Unlock()
	conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := s.GetNode(n.UUID)
	if got.CurrentCycleUsed != 142 || got.InitialUsed != 0 {
		t.Fatalf("lost live traffic or retained offset: %+v", got)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	disk, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if disk[n.UUID].CurrentCycleUsed != 142 || disk[n.UUID].ResetDay != day {
		t.Fatal("recalculation not persisted")
	}
}
