package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"vibemonitor/internal/store"
)

type telegramSender func(context.Context, store.Config, string) error

type telegramRetry struct {
	failures int
	next     time.Time
}

type telegramAlerts struct {
	retry  map[string]telegramRetry
	kind   string
	states map[string]store.TelegramAlertState
	saved  map[string]store.TelegramAlertState
	epoch  string
	send   telegramSender
	load   func(string) (map[string]store.TelegramAlertState, error)
	save   func(string, string, store.TelegramAlertState) error
	remove func(string) error
	now    func() time.Time
}

func newTelegramAlerts(send telegramSender) *telegramAlerts {
	return &telegramAlerts{states: make(map[string]store.TelegramAlertState), saved: make(map[string]store.TelegramAlertState), retry: make(map[string]telegramRetry), send: send, now: time.Now}
}

func (a *telegramAlerts) check(ctx context.Context, cfg store.Config, nodes []*store.Node) {
	if !cfg.TelegramEnabled {
		a.states = make(map[string]store.TelegramAlertState)
		a.saved = make(map[string]store.TelegramAlertState)
		a.epoch = ""
		a.retry = make(map[string]telegramRetry)
		return
	}
	epoch := telegramAlertEpoch(cfg)
	if a.epoch != epoch {
		loaded := make(map[string]store.TelegramAlertState)
		if a.load != nil {
			var err error
			loaded, err = a.load(epoch)
			if err != nil {
				log.Printf("[Telegram] Could not load alert state: %v", err)
				return
			}
		}
		a.states = loaded
		a.saved = make(map[string]store.TelegramAlertState, len(loaded))
		for id, state := range loaded {
			a.saved[id] = state
		}
		a.epoch = epoch
		a.retry = make(map[string]telegramRetry)
	}
	now := a.now()
	zone := cfg.TelegramReminderTimezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		log.Printf("[Telegram] Invalid reminder timezone: %v", err)
		return
	}
	localNow := now.In(location)
	templates := cfg.TelegramTemplates.Effective()
	active := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if ctx.Err() != nil {
			return
		}
		active[node.UUID] = true
		if node.Profile != nil && node.Profile.AlertsDisabled {
			a.deleteState(node.UUID)
			continue
		}
		state := a.states[node.UUID]
		label := alertNodeLabel(node)
		state.Seen = true
		state.Online = node.Online
		a.kind = node.UUID + ":offline"
		if !node.Online && !node.LastSeen.IsZero() && now.Sub(node.LastSeen) >= time.Duration(cfg.TelegramOfflineDelaySeconds)*time.Second && !state.OfflineAlerted {
			message := renderTelegramTemplate(templates.Offline, map[string]string{
				"node": label, "last_seen": node.LastSeen.In(location).Format("2006-01-02 15:04:05"),
			})
			if a.deliver(ctx, cfg, message) {
				state.OfflineAlerted = true
			}
		} else if node.Online && state.OfflineAlerted {
			a.kind = node.UUID + ":recovery"
			if a.deliver(ctx, cfg, renderTelegramTemplate(templates.Recovery, map[string]string{"node": label})) {
				state.OfflineAlerted = false
			}
		}
		if node.Online && node.LastReport != nil {
			if node.Profile != nil && node.Profile.CPUThreshold != nil {
				a.kind = node.UUID + ":cpu"
				if state.CPUThreshold != *node.Profile.CPUThreshold {
					state.CPUHighSince, state.CPU = 0, false
					state.CPUThreshold = *node.Profile.CPUThreshold
				}
				message := renderTelegramTemplate(templates.CPU, map[string]string{
					"node": label, "cpu": fmt.Sprintf("%.1f", node.LastReport.CPU.Usage), "cpu_threshold": fmt.Sprintf("%.1f", *node.Profile.CPUThreshold),
				})
				state.CPU = a.sustainedThreshold(ctx, cfg, state.CPU, node.LastReport.CPU.Usage, *node.Profile.CPUThreshold, &state.CPUHighSince, &state.CPULastAlert, message)
			} else {
				state.CPU = false
				state.CPUHighSince = 0
			}
			a.kind = node.UUID + ":memory"
			memoryMessage := renderTelegramTemplate(templates.Memory, map[string]string{
				"node": label, "memory": fmt.Sprintf("%.1f", memoryPercent(node)), "memory_threshold": "85",
			})
			if node.LastReport.RAM.Total > 0 {
				state.Memory = a.sustainedThreshold(ctx, cfg, state.Memory, memoryPercent(node), 85, &state.MemoryHighSince, &state.MemoryLastAlert, memoryMessage)
			} else {
				state.MemoryHighSince = 0
			}
		}
		if !node.Online {
			state.CPUHighSince, state.MemoryHighSince = 0, 0
		}
		a.kind = node.UUID + ":traffic-warning"
		a.trafficWarning(ctx, cfg, node, &state)
		trafficHigh := node.TrafficLimit > 0 && node.CycleTotalUsed >= node.TrafficLimit
		trafficMessage := renderTelegramTemplate(templates.Traffic, map[string]string{
			"node": label, "used_gib": fmt.Sprintf("%.2f", gib(node.CycleTotalUsed)), "limit_gib": fmt.Sprintf("%.2f", gib(node.TrafficLimit)),
		})
		a.kind = node.UUID + ":traffic"
		state.Traffic = a.threshold(ctx, cfg, state.Traffic, trafficHigh, trafficMessage)
		if cfg.TelegramReminderDays > 0 && node.Profile != nil && node.Profile.DueDate != "" && localNow.Hour() >= cfg.TelegramReminderHour {
			if days, ok := daysUntilDue(node.Profile.DueDate, localNow); ok && days >= 0 && days <= cfg.TelegramReminderDays {
				today := localNow.Format("2006-01-02")
				if state.ReminderDate != today || state.ReminderFor != node.Profile.DueDate {
					message := renderTelegramTemplate(templates.Due, map[string]string{
						"node": label, "due_date": node.Profile.DueDate, "days": fmt.Sprint(days),
					})
					a.kind = node.UUID + ":due"
					if a.deliver(ctx, cfg, message) {
						state.ReminderDate, state.ReminderFor = today, node.Profile.DueDate
					}
				}
			}
		}
		a.states[node.UUID] = state
		a.persistState(node.UUID, state)
	}
	for id := range a.states {
		if !active[id] {
			a.deleteState(id)
		}
	}
}

// Early warnings are delivered at most once per billing cycle. If usage has
// already reached the quota, the existing over-quota alert takes precedence.
func (a *telegramAlerts) trafficWarning(ctx context.Context, cfg store.Config, node *store.Node, state *store.TelegramAlertState) {
	if node.Profile == nil || node.Profile.TrafficWarningPercent <= 0 || node.TrafficLimit <= 0 || node.CycleTotalUsed >= node.TrafficLimit {
		return
	}
	percent := float64(node.CycleTotalUsed) / float64(node.TrafficLimit) * 100
	cycle := node.CycleStart.UTC().Format(time.RFC3339Nano)
	if percent < node.Profile.TrafficWarningPercent || state.TrafficWarningCycle == cycle {
		return
	}
	message := renderTelegramTemplate(cfg.TelegramTemplates.Effective().TrafficWarning, map[string]string{
		"node": alertNodeLabel(node), "used_gib": fmt.Sprintf("%.2f", gib(node.CycleTotalUsed)),
		"limit_gib": fmt.Sprintf("%.2f", gib(node.TrafficLimit)), "percent": fmt.Sprintf("%.1f", percent),
		"warning_percent": fmt.Sprintf("%g", node.Profile.TrafficWarningPercent),
	})
	if a.deliver(ctx, cfg, message) {
		state.TrafficWarningCycle = cycle
	}
}

func renderTelegramTemplate(template string, values map[string]string) string {
	replacements := make([]string, 0, len(values)*2)
	for name, value := range values {
		replacements = append(replacements, "{"+name+"}", value)
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

func telegramAlertEpoch(cfg store.Config) string {
	if cfg.TelegramAlertEpoch != "" {
		return cfg.TelegramAlertEpoch
	}
	sum := sha256.Sum256([]byte(cfg.TelegramBotToken + "\x00" + cfg.TelegramChatID))
	return hex.EncodeToString(sum[:16])
}

func (a *telegramAlerts) persistState(id string, state store.TelegramAlertState) {
	if a.save == nil {
		return
	}
	if previous, ok := a.saved[id]; ok && previous == state {
		return
	}
	if err := a.save(a.epoch, id, state); err != nil {
		log.Printf("[Telegram] Could not save alert state: %v", err)
		return
	}
	a.saved[id] = state
}

func (a *telegramAlerts) deleteState(id string) {
	if a.remove != nil {
		if err := a.remove(id); err != nil {
			log.Printf("[Telegram] Could not remove alert state: %v", err)
			return
		}
	}
	delete(a.states, id)
	delete(a.saved, id)
}

func daysUntilDue(due string, now time.Time) (int, bool) {
	date, err := time.Parse("2006-01-02", due)
	if err != nil {
		return 0, false
	}
	// Compare civil dates in UTC so a local daylight-saving transition cannot
	// turn one calendar day into a 23- or 25-hour interval.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(date.Sub(today).Hours() / 24), true
}

func alertNodeLabel(node *store.Node) string {
	label := node.Name
	if label == "" {
		label = node.UUID
	}
	runes := []rune(label)
	if len(runes) > 100 {
		return string(runes[:100]) + "…"
	}
	return label
}

func memoryPercent(node *store.Node) float64 {
	if node.LastReport == nil || node.LastReport.RAM.Total <= 0 {
		return 0
	}
	return float64(node.LastReport.RAM.Used) / float64(node.LastReport.RAM.Total) * 100
}
func gib(bytes int64) float64 { return float64(bytes) / (1024 * 1024 * 1024) }

func (a *telegramAlerts) threshold(ctx context.Context, cfg store.Config, wasHigh, high bool, message string) bool {
	if !high {
		return false
	}
	if wasHigh {
		return true
	}
	return a.deliver(ctx, cfg, message)
}

// Require 30 seconds above threshold, recover 5 percentage points below it,
// and wait at least five minutes between repeat alerts for the same resource.
func (a *telegramAlerts) sustainedThreshold(ctx context.Context, cfg store.Config, active bool, value, threshold float64, since, last *int64, message string) bool {
	now := a.now().Unix()
	if value < threshold {
		*since = 0
	}
	if value <= max(0, threshold-5) {
		return false
	}
	if active {
		return true
	}
	if value < threshold {
		return false
	}
	if *since == 0 {
		*since = now
		return false
	}
	if now-*since < 30 || (*last > 0 && now-*last < 300) {
		return false
	}
	if a.deliver(ctx, cfg, message) {
		*last = now
		return true
	}
	return false
}

func (a *telegramAlerts) deliver(ctx context.Context, cfg store.Config, message string) bool {
	retry := a.retry[a.kind]
	if a.now().Before(retry.next) {
		return false
	}
	if err := a.send(ctx, cfg, message); err != nil {
		retry.failures++
		delay := min(300, 30<<min(retry.failures-1, 4))
		retry.next = a.now().Add(time.Duration(delay) * time.Second)
		a.retry[a.kind] = retry
		log.Printf("[Telegram] Alert delivery failed; retry in %ds: %v", delay, err)
		return false
	}
	delete(a.retry, a.kind)
	return true
}

func sendTelegramMessage(ctx context.Context, client *http.Client, cfg store.Config, message string) error {
	if err := storeValidateTelegram(cfg); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"chat_id": cfg.TelegramChatID, "text": message})
	url := "https://api.telegram.org/bot" + cfg.TelegramBotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return errors.New("could not create Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Telegram network request failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return errors.New("invalid Telegram response")
	}
	if resp.StatusCode != http.StatusOK || !result.OK {
		return fmt.Errorf("Telegram rejected message (HTTP %d): %s", resp.StatusCode, result.Description)
	}
	return nil
}

func storeValidateTelegram(cfg store.Config) error {
	if cfg.TelegramBotToken == "" || cfg.TelegramChatID == "" {
		return errors.New("Telegram bot token and chat ID are required")
	}
	return nil
}

func telegramHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
