package store

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChatPattern = regexp.MustCompile(`^@[A-Za-z0-9_]{5,32}$`)

type TelegramOptions struct {
	BotToken            string
	ChatID              string
	Enabled             bool
	Clear               bool
	OfflineDelaySeconds *int
	ReminderDays        *int
	ReminderHour        *int
	ReminderTimezone    *string
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
	return nil
}
