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
		{Name: "上海电信", Host: "ct:80"},
		{Name: "上海联通", Host: "cu:80"},
		{Name: "上海移动", Host: "cm:80"},
	}
	results := make([]protocol.PingResult, len(targets))
	for i, target := range targets {
		results[i] = protocol.PingResult{Name: target.Name, Host: target.Host, Method: "tcp", Latency: -1}
	}
	return &store.Node{
		UUID: "sgp", Name: "SGP", Online: true, LastSeen: now,
		Profile: &store.NodeProfile{Targets: targets},
		LastReport: &protocol.Report{PingResults: results},
	}
}

func TestThreeNetworkPingRequiresCompleteCarrierResults(t *testing.T) {
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
		{"missing carrier", func(n *store.Node) { n.Profile.Targets = n.Profile.Targets[:2] }, false, false},
		{"unrelated target", func(n *store.Node) {
			n.Profile.Targets = append(n.Profile.Targets, protocol.PingTarget{Name: "Google", Host: "google:80"})
			n.LastReport.PingResults = append(n.LastReport.PingResults, protocol.PingResult{Name: "Google", Host: "google:80", Latency: 10})
		}, true, true},
		{"invalid negative latency", func(n *store.Node) { n.LastReport.PingResults[0].Latency = -2 }, false, false},
		{"duplicate carrier cannot replace mobile", func(n *store.Node) { n.Profile.Targets[2].Name = "北京电信" }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := pingAlertNode(time.Now())
			tc.change(node)
			_, complete, timeout := threeNetworkPing(node, store.Config{})
			if complete != tc.complete || timeout != tc.timeout {
				t.Fatalf("got complete=%v timeout=%v", complete, timeout)
			}
		})
	}
	node := pingAlertNode(time.Now())
	cfg := store.Config{PingTargets: node.Profile.Targets}
	node.Profile = nil
	if _, complete, timeout := threeNetworkPing(node, cfg); !complete || !timeout {
		t.Fatal("legacy global targets not recognized")
	}
	for i, name := range []string{"China Telecom", "China Unicom", "China Mobile"} {
		if pingCarrier(name) != i {
			t.Fatalf("carrier not recognized: %s", name)
		}
	}
}

func TestThreeNetworkPingDelayRecoveryAndPersistence(t *testing.T) {
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
	if len(messages) != 1 || !strings.Contains(messages[0], "电信、联通、移动同时超时") {
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
	if len(messages) != 2 || !strings.Contains(messages[1], "至少一网已恢复") {
		t.Fatalf("expected one recovery, got %v", messages)
	}
	node.LastReport.PingResults[0].Latency = -1
	check()
	now = now.Add(60 * time.Second)
	check()
	if len(messages) != 3 { t.Fatal("new outage did not rearm") }
}

func TestThreeNetworkPingRetryOptOutAndConfigChange(t *testing.T) {
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
