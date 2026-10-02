package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"vibemonitor/internal/store"
)

func TestTrafficWarningCycleAndRestart(t *testing.T) {
	ctx := context.Background()
	cfg := store.Config{TelegramEnabled: true, TelegramAlertEpoch: "test"}
	cfg.TelegramTemplates.TrafficWarning = "{node}:{percent}:{warning_percent}"
	cycle := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	node := &store.Node{UUID: "node", Name: "Tokyo", TrafficLimit: 1000, InitialUsed: 799,
		CycleStart: cycle, Profile: &store.NodeProfile{TrafficWarningPercent: 80}}
	var messages []string
	send := func(_ context.Context, _ store.Config, message string) error {
		messages = append(messages, message)
		return nil
	}
	saved := make(map[string]store.TelegramAlertState)
	attachPersistence := func(a *telegramAlerts) {
		a.load = func(string) (map[string]store.TelegramAlertState, error) {
			copy := make(map[string]store.TelegramAlertState)
			for id, state := range saved {
				copy[id] = state
			}
			return copy, nil
		}
		a.save = func(_ string, id string, state store.TelegramAlertState) error {
			saved[id] = state
			return nil
		}
	}
	a := newTelegramAlerts(send)
	attachPersistence(a)
	check := func() { node.CycleTotalUsed = node.InitialUsed; a.check(ctx, cfg, []*store.Node{node}) }
	check()
	if len(messages) != 0 {
		t.Fatal("warned below threshold")
	}
	node.InitialUsed = 800
	check()
	check()
	if len(messages) != 1 || messages[0] != "Tokyo:80.0:80" {
		t.Fatalf("expected one rendered warning at threshold, got %v", messages)
	}
	a = newTelegramAlerts(send)
	attachPersistence(a)
	check()
	// A manual correction and another threshold crossing must not repeat the warning.
	node.InitialUsed = 100
	check()
	node.InitialUsed = 900
	check()
	if len(messages) != 1 {
		t.Fatalf("warning repeated after restart or usage correction: %v", messages)
	}
	node.CycleStart = cycle.AddDate(0, 1, 0)
	check()
	if len(messages) != 2 {
		t.Fatal("new billing cycle did not rearm warning")
	}
	node.InitialUsed = 1000
	check()
	check()
	if len(messages) != 3 || !strings.Contains(messages[2], "流量告警") {
		t.Fatalf("expected existing over-quota alert once, got %v", messages)
	}
}

func TestTrafficWarningDisabledAndOverQuota(t *testing.T) {
	for _, tc := range []struct {
		name string
		profile *store.NodeProfile
		limit, used int64
		want int
	}{
		{"legacy", nil, 1000, 800, 0},
		{"zero", &store.NodeProfile{}, 1000, 800, 0},
		{"no quota", &store.NodeProfile{TrafficWarningPercent: 80}, 0, 800, 0},
		{"node disabled", &store.NodeProfile{TrafficWarningPercent: 80, AlertsDisabled: true}, 1000, 800, 0},
		{"over quota", &store.NodeProfile{TrafficWarningPercent: 80}, 1000, 1100, 1},
		{"100 percent", &store.NodeProfile{TrafficWarningPercent: 100}, 1000, 1000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []string
			a := newTelegramAlerts(func(_ context.Context, _ store.Config, message string) error {
				messages = append(messages, message)
				return nil
			})
			cfg := store.Config{TelegramEnabled: true}
			node := &store.Node{UUID: "node", TrafficLimit: tc.limit, CycleTotalUsed: tc.used, Profile: tc.profile}
			a.check(context.Background(), cfg, []*store.Node{node})
			a.check(context.Background(), cfg, []*store.Node{node})
			if len(messages) != tc.want {
				t.Fatalf("got %v, want %d alerts", messages, tc.want)
			}
			if tc.want > 0 && strings.Contains(messages[0], "提前预警") {
				t.Fatal("early warning duplicated over-quota alert")
			}
		})
	}
}

func TestTrafficWarningRetryAfterDeliveryFailure(t *testing.T) {
	attempts := 0
	a := newTelegramAlerts(func(context.Context, store.Config, string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary failure")
		}
		return nil
	})
	cfg := store.Config{TelegramEnabled: true}
	node := &store.Node{UUID: "node", TrafficLimit: 1000, CycleTotalUsed: 800,
		Profile: &store.NodeProfile{TrafficWarningPercent: 80}}
	for i := 0; i < 3; i++ {
		a.check(context.Background(), cfg, []*store.Node{node})
	}
	if attempts != 2 {
		t.Fatalf("got %d attempts, want failed attempt plus successful retry", attempts)
	}
	cfg.TelegramEnabled = false
	node.CycleStart = time.Now()
	a.check(context.Background(), cfg, []*store.Node{node})
	if attempts != 2 {
		t.Fatal("sent warning with Telegram disabled")
	}
}
