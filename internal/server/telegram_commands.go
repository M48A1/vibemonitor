package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vibemonitor/internal/store"
)

type telegramUpdate struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			ID    int64 `json:"id"`
			IsBot bool  `json:"is_bot"`
		} `json:"from"`
	} `json:"message"`
}

func privateTelegramCommand(update telegramUpdate, chatID int64) []string {
	m := update.Message
	if chatID <= 0 || m == nil || m.Chat.Type != "private" || m.Chat.ID != chatID ||
		m.From.ID != chatID || m.From.IsBot {
		return nil
	}
	return strings.Fields(m.Text)
}

func getTelegramUpdates(ctx context.Context, client *http.Client, token string, offset int64) ([]telegramUpdate, error) {
	data, err := json.Marshal(struct {
		Offset         int64    `json:"offset"`
		Limit          int      `json:"limit"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{offset, 10, 20, []string{"message"}})
	if err != nil {
		return nil, err
	}
	url := "https://api.telegram.org/bot" + token + "/getUpdates"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("could not create Telegram command request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Telegram command request failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Telegram command request rejected (HTTP %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || !result.OK {
		return nil, errors.New("invalid Telegram command response")
	}
	return result.Result, nil
}

func (s *Server) runTelegramCommands(ctx context.Context) {
	pollClient := telegramHTTPClient()
	pollClient.Timeout = 28 * time.Second
	sendClient := telegramHTTPClient()
	send := func(ctx context.Context, cfg store.Config, text string) error {
		return sendTelegramMessage(ctx, sendClient, cfg, text)
	}
	var token string
	var offset int64
	for ctx.Err() == nil {
		cfg := s.store.GetConfig()
		chatID, err := strconv.ParseInt(cfg.TelegramChatID, 10, 64)
		if err == nil && chatID > 0 && cfg.TelegramBotToken != "" {
			if token != cfg.TelegramBotToken {
				token, offset = cfg.TelegramBotToken, 0
			}
			updates, err := getTelegramUpdates(ctx, pollClient, token, offset)
			if err == nil {
				if len(updates) == 0 {
					// The last offset was confirmed. Allow Telegram to choose the
					// next ID if its sequence resets after a long period of inactivity.
					offset = 0
				}
				for _, update := range updates {
					if ctx.Err() != nil {
						return
					}
					if command := privateTelegramCommand(update, chatID); len(command) > 0 {
						if err := s.replyTelegramReport(ctx, cfg, time.Now(), command, send); err != nil && ctx.Err() == nil {
							log.Printf("[Telegram commands] Report reply failed: %v", err)
						}
					}
					offset = max(offset, update.ID+1)
				}
				continue
			}
			if ctx.Err() == nil {
				log.Printf("[Telegram commands] %v", err)
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Server) replyTelegramReport(ctx context.Context, cfg store.Config, now time.Time, command []string, send telegramSender) error {
	var lastSend time.Time
	sendCurrent := func(text string) error {
		if delay := time.Until(lastSend.Add(time.Second)); delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		current := s.store.GetConfig()
		if current.TelegramBotToken != cfg.TelegramBotToken || current.TelegramChatID != cfg.TelegramChatID {
			return errors.New("Telegram command configuration changed")
		}
		lastSend = time.Now()
		return send(ctx, cfg, text)
	}
	switch command[0] {
	case "/dailyreport":
		if len(command) == 1 {
			return s.replyDailyReportCommand(ctx, now, sendCurrent)
		}
	case "/report":
		return s.replyArchivedReportCommand(ctx, now, command[1:], sendCurrent)
	default:
		if _, err := parseReportDate(command[0]); err == nil {
			return s.replyArchivedReportCommand(ctx, now, command, sendCurrent)
		}
	}
	return nil
}

func (s *Server) replyDailyReportCommand(ctx context.Context, now time.Time, send func(string) error) error {
	report, err := s.store.LatestArchivedDailyReportContext(ctx, now)
	if err != nil {
		_ = send("暂时无法读取日报，请稍后重试。")
		return err
	}
	if report.Date == "" {
		return send("暂无已存档的完整日报。")
	}
	return sendArchivedDailyReport(report, send)
}
