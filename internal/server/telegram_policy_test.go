package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"vibemonitor/internal/store"
)

func TestResourceAlertDebounceHysteresisCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	sent := 0
	a := newTelegramAlerts(func(context.Context, store.Config, string) error { sent++; return nil })
	a.now = func() time.Time { return now }
	a.kind = "cpu"
	var since, last int64
	active := false
	check := func(value float64) {
		active = a.sustainedThreshold(context.Background(), store.Config{}, active, value, 80, &since, &last, "test")
	}
	check(81)
	now = now.Add(20 * time.Second)
	check(79)
	check(81)
	now = now.Add(29 * time.Second)
	check(81)
	if sent != 0 {
		t.Fatal("short spike alerted")
	}
	now = now.Add(time.Second)
	check(81)
	if sent != 1 {
		t.Fatal("sustained load not alerted")
	}
	check(79)
	check(81)
	if sent != 1 || !active {
		t.Fatal("threshold jitter rearmed")
	}
	check(74)
	if active {
		t.Fatal("recovery hysteresis failed")
	}
	check(90)
	now = now.Add(31 * time.Second)
	check(90)
	if sent != 1 {
		t.Fatal("cooldown ignored")
	}
	now = now.Add(270 * time.Second)
	check(90)
	if sent != 2 {
		t.Fatal("repeat alert never rearmed")
	}
}

func TestTelegramBackoffAndDeliveryAcknowledgement(t *testing.T) {
	now := time.Unix(1000, 0)
	calls := 0
	fail := true
	a := newTelegramAlerts(func(context.Context, store.Config, string) error {
		calls++
		if fail {
			return errors.New("offline")
		}
		return nil
	})
	a.now = func() time.Time { return now }
	a.kind = "node:offline"
	if a.deliver(context.Background(), store.Config{}, "test") {
		t.Fatal("failed send acknowledged")
	}
	a.deliver(context.Background(), store.Config{}, "test")
	if calls != 1 {
		t.Fatal("no backoff")
	}
	now = now.Add(30 * time.Second)
	a.deliver(context.Background(), store.Config{}, "test")
	now = now.Add(59 * time.Second)
	a.deliver(context.Background(), store.Config{}, "test")
	if calls != 2 {
		t.Fatal("exponential backoff ignored")
	}
	now = now.Add(time.Second)
	fail = false
	if !a.deliver(context.Background(), store.Config{}, "test") || calls != 3 {
		t.Fatal("retry did not succeed")
	}
}

func TestTelegramQueueAllowsOtherNodesAndStops(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	days, hour := 7, 0
	zone := "UTC"
	if err := s.UpdateTelegramOptions(store.TelegramOptions{BotToken: "123:abc", ChatID: "123", Enabled: true, ReminderDays: &days, ReminderHour: &hour, ReminderTimezone: &zone}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"slow", "fast1", "fast2", "fast3", "fast4"} {
		if _, err := s.CreateNodeWithOptions(store.NodeOptions{Name: name, Profile: &store.NodeProfile{DueDate: time.Now().UTC().Format("2006-01-02")}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 10)
	finished := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fast := make(chan struct{}, 10)
	var inFlight, maximum atomic.Int32
	server := &Server{store: s}
	go func() {
		defer close(finished)
		server.runTelegramQueue(ctx, ticks, func(ctx context.Context, _ store.Config, message string) error {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				old := maximum.Load()
				if n <= old || maximum.CompareAndSwap(old, n) {
					break
				}
			}
			if strings.Contains(message, "slow") {
				slowStarted <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			fast <- struct{}{}
			return nil
		})
	}()
	ticks <- time.Now()
	select {
	case <-slowStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("slow node not started")
	}
	for i := 0; i < 4; i++ {
		select {
		case <-fast:
		case <-time.After(3 * time.Second):
			t.Fatal("slow node blocked other nodes")
		}
	}
	if maximum.Load() > telegramWorkers {
		t.Fatal("unbounded workers")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not shut down")
	}
}
