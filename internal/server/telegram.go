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
	"time"

	"vibemonitor/internal/store"
)

type telegramSender func(context.Context, store.Config, string) error

type telegramAlerts struct {
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
	return &telegramAlerts{states: make(map[string]store.TelegramAlertState), saved: make(map[string]store.TelegramAlertState), send: send, now: time.Now}
}

func (a *telegramAlerts) check(ctx context.Context, cfg store.Config, nodes []*store.Node) {
	if !cfg.TelegramEnabled {
		a.states = make(map[string]store.TelegramAlertState)
		a.saved = make(map[string]store.TelegramAlertState)
		a.epoch = ""
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
		if !node.Online && !node.LastSeen.IsZero() && now.Sub(node.LastSeen) >= time.Duration(cfg.TelegramOfflineDelaySeconds)*time.Second && !state.OfflineAlerted {
			if a.deliver(ctx, cfg, fmt.Sprintf("🔴 VibeMonitor 节点离线：%s（最后上报：%s）", label, node.LastSeen.In(location).Format("2006-01-02 15:04:05"))) {
				state.OfflineAlerted = true
			}
		} else if node.Online && state.OfflineAlerted {
			if a.deliver(ctx, cfg, fmt.Sprintf("🟢 VibeMonitor 节点恢复：%s", label)) {
				state.OfflineAlerted = false
			}
		}
		if node.Online && node.LastReport != nil {
			if node.Profile != nil && node.Profile.CPUThreshold != nil {
				cpuHigh := node.LastReport.CPU.Usage >= *node.Profile.CPUThreshold
				state.CPU = a.threshold(ctx, cfg, state.CPU, cpuHigh, fmt.Sprintf("⚠️ VibeMonitor CPU 告警：%s，当前 %.1f%%，阈值 %.1f%%", label, node.LastReport.CPU.Usage, *node.Profile.CPUThreshold))
			} else {
				state.CPU = false
			}
			memoryHigh := node.LastReport.RAM.Total > 0 && float64(node.LastReport.RAM.Used)/float64(node.LastReport.RAM.Total) >= 0.85
			state.Memory = a.threshold(ctx, cfg, state.Memory, memoryHigh, fmt.Sprintf("⚠️ VibeMonitor 内存告警：%s，使用率 %.1f%%，阈值 85%%", label, memoryPercent(node)))
		}
		trafficHigh := node.TrafficLimit > 0 && node.CycleTotalUsed >= node.TrafficLimit
		state.Traffic = a.threshold(ctx, cfg, state.Traffic, trafficHigh, fmt.Sprintf("⚠️ VibeMonitor 流量告警：%s，本周期已用 %.2f GiB，额度 %.2f GiB", label, gib(node.CycleTotalUsed), gib(node.TrafficLimit)))
		if cfg.TelegramReminderDays > 0 && node.Profile != nil && node.Profile.DueDate != "" && localNow.Hour() >= cfg.TelegramReminderHour {
			if days, ok := daysUntilDue(node.Profile.DueDate, localNow); ok && days >= 0 && days <= cfg.TelegramReminderDays {
				today := localNow.Format("2006-01-02")
				if state.ReminderDate != today || state.ReminderFor != node.Profile.DueDate {
					if a.deliver(ctx, cfg, fmt.Sprintf("📅 VibeMonitor 到期提醒：%s，%s 到期（剩余 %d 天）", label, node.Profile.DueDate, days)) {
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

func (a *telegramAlerts) deliver(ctx context.Context, cfg store.Config, message string) bool {
	if err := a.send(ctx, cfg, message); err != nil {
		log.Printf("[Telegram] Alert delivery failed: %v", err)
		return false
	}
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

func (s *Server) runTelegramAlerts(ctx context.Context) {
	client := telegramHTTPClient()
	alerts := newTelegramAlerts(func(ctx context.Context, cfg store.Config, text string) error {
		return sendTelegramMessage(ctx, client, cfg, text)
	})
	alerts.load = s.store.LoadTelegramAlertStates
	alerts.save = s.store.SaveTelegramAlertState
	alerts.remove = s.store.DeleteTelegramAlertState
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := s.store.GetConfig()
			if cfg.TelegramEnabled {
				alerts.check(ctx, cfg, s.store.GetAlertNodes())
			} else {
				alerts.check(ctx, cfg, nil)
			}
		}
	}
}
