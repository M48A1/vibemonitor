package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"vibemonitor/internal/store"
)

// Require a result for every configured target in this report. Missing
// results are unknown, never a timeout or a recovery. Match hosts as well as
// names so results from a previous configuration cannot affect this alert.
func allTargetsPing(node *store.Node, cfg store.Config) (key string, complete, allTimeout bool) {
	targets := cfg.PingTargets
	if node.Profile != nil {
		targets = node.Profile.Targets
	}
	var identities []string
	for _, target := range targets {
		identities = append(identities, fmt.Sprintf("%q:%q", target.Name, target.Host))
	}
	sort.Strings(identities)
	key = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(identities, "\n"))))
	if len(targets) == 0 || node.LastReport == nil {
		return key, false, false
	}
	allTimeout = true
	for _, target := range targets {
		found := false
		for _, result := range node.LastReport.PingResults {
			if result.Name != target.Name || result.Host != target.Host {
				continue
			}
			if result.Latency < -1 {
				return key, false, false
			}
			found = true
			if result.Latency >= 0 {
				allTimeout = false
			}
			break
		}
		if !found {
			return key, false, false
		}
	}
	return key, true, allTimeout
}

func (a *telegramAlerts) checkAllTargetsPing(ctx context.Context, cfg store.Config, node *store.Node, state *store.TelegramAlertState, location *time.Location) {
	key, complete, allTimeout := allTargetsPing(node, cfg)
	if state.PingTargetsKey != key {
		state.PingTargetsKey = key
		state.PingTimeoutSince = 0
		state.PingTimeoutAlerted = false
		delete(a.retry, node.UUID+":ping-timeout")
		delete(a.retry, node.UUID+":ping-recovery")
	}
	// Do not announce stale probe results while the agent itself is offline.
	if !node.Online || node.LastSeen.IsZero() || !complete {
		state.PingTimeoutSince = 0
		return
	}
	if !allTimeout {
		state.PingTimeoutSince = 0
		delete(a.retry, node.UUID+":ping-timeout")
		if state.PingTimeoutAlerted {
			a.kind = node.UUID + ":ping-recovery"
			message := fmt.Sprintf("🟢 VibeMonitor Ping 探测恢复：%s（至少一个测试目标已恢复，最后上报：%s）", alertNodeLabel(node), node.LastSeen.In(location).Format("2006-01-02 15:04:05"))
			if a.deliver(ctx, cfg, message) {
				state.PingTimeoutAlerted = false
			}
		}
		return
	}
	delete(a.retry, node.UUID+":ping-recovery")
	now := a.now()
	if state.PingTimeoutSince == 0 {
		state.PingTimeoutSince = now.Unix()
	}
	if state.PingTimeoutAlerted || now.Sub(time.Unix(state.PingTimeoutSince, 0)) < time.Duration(cfg.TelegramOfflineDelaySeconds)*time.Second {
		return
	}
	a.kind = node.UUID + ":ping-timeout"
	message := fmt.Sprintf("🔴 VibeMonitor Ping 探测离线：%s（全部测试目标无回应，最后上报：%s）", alertNodeLabel(node), node.LastSeen.In(location).Format("2006-01-02 15:04:05"))
	if a.deliver(ctx, cfg, message) {
		state.PingTimeoutAlerted = true
	}
}
