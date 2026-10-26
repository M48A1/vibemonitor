package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

func pingAlertNode(now time.Time) *store.Node {
	targets := []protocol.PingTarget{
		{Name: "目标 A", Host: "ct:80"},
		{Name: "目标 B", Host: "cu:80"},
		{Name: "目标 C", Host: "cm:80"},
	}
	results := make([]protocol.PingResult, len(targets))
	for i, target := range targets {
		results[i] = protocol.PingResult{MeasuredAt: now.Unix(), Name: target.Name, Host: target.Host, Method: "tcp", Latency: -1}
	}
	return &store.Node{
		UUID: "sgp", Name: "SGP", Online: true, LastSeen: now,
		Profile: &store.NodeProfile{Targets: targets},
		LastReport: &protocol.Report{PingResults: results},
	}
}

func TestAllTargetsPingRequiresCompleteResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		change func(*store.Node)
		complete, timeout bool
	}{
		{"all timeout", func(n *store.Node) {}, true, true},
		{"one recovered", func(n *store.Node) { n.LastReport.PingResults[1].Latency = 0 }, true, false},
		{"missing result", func(n *store.Node) { n.LastReport.PingResults = n.LastReport.PingResults[:2] }, false, false},
		{"no report", func(n *store.Node) { n.LastReport = nil }, false, false},
		{"old host", func(n *store.Node) { n.LastReport.PingResults[0].Host = "old:80" }, false, false},
		{"two configured targets", func(n *store.Node) { n.Profile.Targets = n.Profile.Targets[:2] }, true, true},
		{"unrelated target", func(n *store.Node) {
			n.Profile.Targets = append(n.Profile.Targets, protocol.PingTarget{Name: "Google", Host: "google:80"})
			n.LastReport.PingResults = append(n.LastReport.PingResults, protocol.PingResult{MeasuredAt: time.Now().Unix(), Name: "Google", Host: "google:80", Latency: 10})
		}, true, false},
		{"invalid negative latency", func(n *store.Node) { n.LastReport.PingResults[0].Latency = -2 }, false, false},
		{"no configured targets", func(n *store.Node) { n.Profile.Targets = nil }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := pingAlertNode(time.Now())
			tc.change(node)
			_, complete, timeout, _ := allTargetsPing(node, store.Config{}, time.Now())
			if complete != tc.complete || timeout != tc.timeout {
				t.Fatalf("got complete=%v timeout=%v", complete, timeout)
			}
		})
	}
	node := pingAlertNode(time.Now())
	cfg := store.Config{PingTargets: node.Profile.Targets}
	node.Profile = nil
	if _, complete, timeout, _ := allTargetsPing(node, cfg, time.Now()); !complete || !timeout {
		t.Fatal("legacy global targets not recognized")
	}
}

func TestAllTargetsPingDelayRecoveryAndPersistence(t *testing.T) {
	now := time.Unix(1000, 0)
	cfg := store.Config{TelegramEnabled: true, TelegramOfflineDelaySeconds: 60}
	node := pingAlertNode(now)
	var messages []string
	send := func(_ context.Context, _ store.Config, message string) error {
		messages = append(messages, message)
		return nil
	}
	a := newTelegramAlerts(send)
	a.now = func() time.Time { return now }
	check := func() {
		node.LastSeen = now
		for i := range node.LastReport.PingResults { node.LastReport.PingResults[i].MeasuredAt = now.Unix() }
		a.check(context.Background(), cfg, []*store.Node{node})
	}
	check()
	now = now.Add(59 * time.Second)
	check()
	if len(messages) != 0 {
		t.Fatal("alert sent before delay")
	}
	now = now.Add(time.Second)
	check()
	check()
	if len(messages) != 1 || !strings.Contains(messages[0], "全部测试目标无回应") {
		t.Fatalf("expected one timeout notification, got %v", messages)
	}
	// Exercise the JSON representation used by the existing SQLite persistence.
	raw, err := json.Marshal(a.states[node.UUID])
	if err != nil { t.Fatal(err) }
	var restored store.TelegramAlertState
	if err := json.Unmarshal(raw, &restored); err != nil { t.Fatal(err) }
	a = newTelegramAlerts(send)
	a.now = func() time.Time { return now }
	a.load = func(string) (map[string]store.TelegramAlertState, error) {
		return map[string]store.TelegramAlertState{node.UUID: restored}, nil
	}
	check()
	if len(messages) != 1 { t.Fatal("restart repeated delivered alert") }
	results := node.LastReport.PingResults
	node.LastReport.PingResults = nil
	check()
	if len(messages) != 1 { t.Fatal("missing results announced recovery") }
	node.LastReport.PingResults = results
	node.LastReport.PingResults[0].Latency = 25
	check()
	check()
	if len(messages) != 2 || !strings.Contains(messages[1], "至少一个测试目标已恢复") {
		t.Fatalf("expected one recovery, got %v", messages)
	}
	node.LastReport.PingResults[0].Latency = -1
	check()
	now = now.Add(60 * time.Second)
	check()
	if len(messages) != 3 { t.Fatal("new outage did not rearm") }
}

func TestAllTargetsPingRetryOptOutAndConfigChange(t *testing.T) {
	now := time.Unix(1000, 0)
	cfg := store.Config{TelegramEnabled: true}
	node := pingAlertNode(now)
	calls := 0
	fail := true
	a := newTelegramAlerts(func(context.Context, store.Config, string) error {
		calls++
		if fail { return errors.New("network failure") }
		return nil
	})
	a.now = func() time.Time { return now }
	check := func() { a.check(context.Background(), cfg, []*store.Node{node}) }
	check()
	check()
	if calls != 1 || a.states[node.UUID].PingTimeoutAlerted {
		t.Fatal("failed send acknowledged or retry backoff skipped")
	}
	fail = false
	now = now.Add(30 * time.Second)
	node.LastSeen = now
	check()
	if calls != 2 || !a.states[node.UUID].PingTimeoutAlerted {
		t.Fatal("notification not retried")
	}
	// Changing a target invalidates the old alert without a fake recovery.
	node.Profile.Targets[0].Host = "new:80"
	check()
	if calls != 2 || a.states[node.UUID].PingTimeoutAlerted {
		t.Fatal("configuration change retained old alert")
	}
	node.LastReport.PingResults[0].Host = "new:80"
	node.Profile.AlertsDisabled = true
	check()
	if calls != 2 { t.Fatal("disabled node alerted") }
	node.Profile.AlertsDisabled = false
	node.Online = false
	node.LastSeen = time.Time{} // No prior report: existing offline alert also skips it.
	check()
	if calls != 2 { t.Fatal("offline node emitted stale ping alert") }
	node.Online = true
	node.LastSeen = now
	cfg.TelegramEnabled = false
	check()
	if calls != 2 { t.Fatal("disabled Telegram alerted") }
}

func TestAllTargetsPingNotificationForOneTwoAndThreeTargets(t *testing.T) {
	for count := 1; count <= 3; count++ {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			now := time.Unix(1000, 0)
			node := pingAlertNode(now)
			node.Profile.Targets = node.Profile.Targets[:count]
			node.LastReport.PingResults = node.LastReport.PingResults[:count]
			var messages []string
			a := newTelegramAlerts(func(_ context.Context, _ store.Config, message string) error {
				messages = append(messages, message)
				return nil
			})
			cfg := store.Config{TelegramEnabled: true}
			a.now = func() time.Time { return now }
			check := func() { a.check(context.Background(), cfg, []*store.Node{node}) }
			check()
			check()
			if len(messages) != 1 || !strings.Contains(messages[0], "全部测试目标无回应") {
				t.Fatalf("expected one outage for %d targets, got %v", count, messages)
			}
			node.LastReport.PingResults[count-1].Latency = 10
			check()
			check()
			if len(messages) != 2 || !strings.Contains(messages[1], "至少一个测试目标已恢复") {
				t.Fatalf("expected one recovery for %d targets, got %v", count, messages)
			}
		})
	}
}

func TestPingCachedResultsDoNotAdvanceAlertDelay(t *testing.T) {
	now := time.Unix(1000, 0)
	node := pingAlertNode(now)
	calls := 0
	a := newTelegramAlerts(func(context.Context, store.Config, string) error { calls++; return nil })
	a.now = func() time.Time { return now }
	cfg := store.Config{TelegramEnabled: true, TelegramOfflineDelaySeconds: 60}
	check := func() {
		node.LastSeen = now // New heartbeat, same cached probe results.
		a.check(context.Background(), cfg, []*store.Node{node})
	}
	check()
	now = now.Add(60 * time.Second)
	check()
	if calls != 0 { t.Fatal("cached failure advanced the outage clock") }
	for i := range node.LastReport.PingResults { node.LastReport.PingResults[i].MeasuredAt = now.Unix() }
	check()
	if calls != 1 { t.Fatal("new failed measurements did not confirm outage") }
	now = now.Add(121 * time.Second)
	node.LastReport.PingResults[0].Latency = 10
	check()
	if calls != 1 { t.Fatal("expired success announced recovery") }
	for i := range node.LastReport.PingResults { node.LastReport.PingResults[i].MeasuredAt = now.Unix() }
	check()
	if calls != 2 { t.Fatal("fresh success did not recover") }
}

func TestPingUnknownMeasurementTimes(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, at := range []int64{0, -1, now.Add(-121*time.Second).Unix(), now.Add(6*time.Second).Unix()} {
		node := pingAlertNode(now)
		node.LastReport.PingResults[0].MeasuredAt = at
		if _, complete, _, _ := allTargetsPing(node, store.Config{}, now); complete {
			t.Fatalf("measurement %d should be unknown", at)
		}
	}
}
