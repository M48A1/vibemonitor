package store

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChatPattern = regexp.MustCompile(`^@[A-Za-z0-9_]{5,32}$`)

type TelegramTemplates struct {
	Offline  string `json:"offline"`
	Recovery string `json:"recovery"`
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`
	Traffic  string `json:"traffic"`
	Due      string `json:"due"`
}

func DefaultTelegramTemplates() TelegramTemplates {
	return TelegramTemplates{
		Offline:  "🔴 VibeMonitor 节点离线：{node}（最后上报：{last_seen}）",
		Recovery: "🟢 VibeMonitor 节点恢复：{node}",
		CPU:      "⚠️ VibeMonitor CPU 告警：{node}，当前 {cpu}%，阈值 {cpu_threshold}%",
		Memory:   "⚠️ VibeMonitor 内存告警：{node}，使用率 {memory}%，阈值 {memory_threshold}%",
		Traffic:  "⚠️ VibeMonitor 流量告警：{node}，本周期已用 {used_gib} GiB，额度 {limit_gib} GiB",
		Due:      "📅 VibeMonitor 到期提醒：{node}，{due_date} 到期（剩余 {days} 天）",
	}
}

// Empty fields mean the original message for that alert type.
func (t TelegramTemplates) Effective() TelegramTemplates {
	defaults := DefaultTelegramTemplates()
	if strings.TrimSpace(t.Offline) == "" {
		t.Offline = defaults.Offline
	}
	if strings.TrimSpace(t.Recovery) == "" {
		t.Recovery = defaults.Recovery
	}
	if strings.TrimSpace(t.CPU) == "" {
		t.CPU = defaults.CPU
	}
	if strings.TrimSpace(t.Memory) == "" {
		t.Memory = defaults.Memory
	}
	if strings.TrimSpace(t.Traffic) == "" {
		t.Traffic = defaults.Traffic
	}
	if strings.TrimSpace(t.Due) == "" {
		t.Due = defaults.Due
	}
	return t
}

func validateTelegramTemplates(t TelegramTemplates) error {
	for _, item := range []struct {
		name, value string
		variables   string
	}{
		{"offline", t.Offline, "node,last_seen"},
		{"recovery", t.Recovery, "node"},
		{"cpu", t.CPU, "node,cpu,cpu_threshold"},
		{"memory", t.Memory, "node,memory,memory_threshold"},
		{"traffic", t.Traffic, "node,used_gib,limit_gib"},
		{"due", t.Due, "node,due_date,days"},
	} {
		if len(item.value) > 2048 {
			return fmt.Errorf("Telegram %s template exceeds 2048 bytes", item.name)
		}
		allowed := make(map[string]bool)
		for _, name := range strings.Split(item.variables, ",") {
			allowed[name] = true
		}
		placeholders := 0
		for i := 0; i < len(item.value); {
			switch item.value[i] {
			case '{':
				end := strings.IndexByte(item.value[i+1:], '}')
				if end < 0 || !allowed[item.value[i+1:i+1+end]] {
					return fmt.Errorf("invalid variable in Telegram %s template; allowed: %s", item.name, item.variables)
				}
				placeholders++
				if placeholders > 16 {
					return fmt.Errorf("too many variables in Telegram %s template", item.name)
				}
				i += end + 2
			case '}':
				return fmt.Errorf("unexpected } in Telegram %s template", item.name)
			default:
				i++
			}
		}
		for _, r := range item.value {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return fmt.Errorf("invalid control character in Telegram %s template", item.name)
			}
		}
	}
	return nil
}

type TelegramOptions struct {
	BotToken            string
	ChatID              string
	Enabled             bool
	Clear               bool
	OfflineDelaySeconds *int
	ReminderDays        *int
	ReminderHour        *int
	ReminderTimezone    *string
	Templates           *TelegramTemplates
}

// UpdateTelegramConfig retains the original API for callers that do not change alert policy.
func (s *Store) UpdateTelegramConfig(token, chatID string, enabled, clear bool) error {
	return s.UpdateTelegramOptions(TelegramOptions{BotToken: token, ChatID: chatID, Enabled: enabled, Clear: clear})
}

// UpdateTelegramOptions keeps an omitted bot token and validates all alert settings.
func (s *Store) UpdateTelegramOptions(opts TelegramOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	if opts.Clear {
		next.TelegramBotToken = ""
		next.TelegramChatID = ""
		next.TelegramEnabled = false
	} else {
		if opts.BotToken != "" {
			next.TelegramBotToken = strings.TrimSpace(opts.BotToken)
		}
		next.TelegramChatID = strings.TrimSpace(opts.ChatID)
		next.TelegramEnabled = opts.Enabled
		if opts.OfflineDelaySeconds != nil {
			next.TelegramOfflineDelaySeconds = *opts.OfflineDelaySeconds
		}
		if opts.ReminderDays != nil {
			next.TelegramReminderDays = *opts.ReminderDays
		}
		if opts.ReminderHour != nil {
			next.TelegramReminderHour = *opts.ReminderHour
		}
		if opts.ReminderTimezone != nil {
			next.TelegramReminderTimezone = strings.TrimSpace(*opts.ReminderTimezone)
		}
		if opts.Templates != nil {
			next.TelegramTemplates = *opts.Templates
		}
		if err := validateTelegramConfig(next); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
		}
	}
	if next.TelegramBotToken != s.config.TelegramBotToken || next.TelegramChatID != s.config.TelegramChatID || next.TelegramEnabled != s.config.TelegramEnabled {
		next.TelegramAlertEpoch = GenerateToken(8)
	}
	return s.commitConfigLocked(next)
}

func validateTelegramConfig(c Config) error {
	if c.TelegramBotToken != "" && (len(c.TelegramBotToken) > 256 || !telegramTokenPattern.MatchString(c.TelegramBotToken)) {
		return errors.New("invalid Telegram bot token")
	}
	if c.TelegramChatID != "" {
		if len(c.TelegramChatID) > 64 {
			return errors.New("invalid Telegram chat ID")
		}
		if !telegramChatPattern.MatchString(c.TelegramChatID) {
			if _, err := strconv.ParseInt(c.TelegramChatID, 10, 64); err != nil {
				return errors.New("invalid Telegram chat ID")
			}
		}
	}
	if c.TelegramEnabled && (c.TelegramBotToken == "" || c.TelegramChatID == "") {
		return errors.New("Telegram bot token and chat ID are required when alerts are enabled")
	}
	if c.TelegramOfflineDelaySeconds < 0 || c.TelegramOfflineDelaySeconds > 3600 {
		return errors.New("Telegram offline delay must be between 0 and 3600 seconds")
	}
	if c.TelegramReminderDays < 0 || c.TelegramReminderDays > 7 {
		return errors.New("Telegram reminder days must be between 0 and 7")
	}
	if c.TelegramReminderHour < 0 || c.TelegramReminderHour > 23 {
		return errors.New("Telegram reminder hour must be between 0 and 23")
	}
	if c.TelegramReminderTimezone != "" {
		if len(c.TelegramReminderTimezone) > 64 {
			return errors.New("invalid Telegram reminder timezone")
		}
		if _, err := time.LoadLocation(c.TelegramReminderTimezone); err != nil {
			return errors.New("invalid Telegram reminder timezone")
		}
	}
	return validateTelegramTemplates(c.TelegramTemplates)
}
