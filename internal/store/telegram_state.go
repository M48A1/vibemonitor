package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// TelegramAlertState records transitions that have already been observed or delivered.
type TelegramAlertState struct {
	PingTimeoutSince    int64   `json:"ping_timeout_since,omitempty"`
	PingTimeoutAlerted  bool    `json:"ping_timeout_alerted,omitempty"`
	PingTargetsKey      string  `json:"ping_targets_key,omitempty"`
	CPUHighSince        int64   `json:"cpu_high_since,omitempty"`
	MemoryHighSince     int64   `json:"memory_high_since,omitempty"`
	CPULastAlert        int64   `json:"cpu_last_alert,omitempty"`
	MemoryLastAlert     int64   `json:"memory_last_alert,omitempty"`
	CPUThreshold        float64 `json:"cpu_threshold,omitempty"`
	Seen                bool    `json:"seen"`
	Online              bool    `json:"online"`
	OfflineAlerted      bool    `json:"offline_alerted"`
	CPU                 bool    `json:"cpu"`
	Memory              bool    `json:"memory"`
	Traffic             bool    `json:"traffic"`
	TrafficWarningCycle string  `json:"traffic_warning_cycle,omitempty"`
	ReminderDate        string  `json:"reminder_date,omitempty"`
	ReminderFor         string  `json:"reminder_for,omitempty"`
}

func (s *Store) LoadTelegramAlertStates(epoch string) (map[string]TelegramAlertState, error) {
	return s.LoadTelegramAlertStatesContext(context.Background(), epoch)
}

func (s *Store) LoadTelegramAlertStatesContext(ctx context.Context, epoch string) (map[string]TelegramAlertState, error) {
	rows, err := s.sdb.reader().QueryContext(ctx, "SELECT node_uuid, state_json FROM telegram_alert_state WHERE config_epoch = ?", epoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]TelegramAlertState)
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var state TelegramAlertState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return nil, fmt.Errorf("invalid Telegram alert state for %s: %w", id, err)
		}
		states[id] = state
	}
	return states, rows.Err()
}

func (s *Store) SaveTelegramAlertState(epoch, id string, state TelegramAlertState) error {
	return s.SaveTelegramAlertStateContext(context.Background(), epoch, id, state)
}

func (s *Store) SaveTelegramAlertStateContext(ctx context.Context, epoch, id string, state TelegramAlertState) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.RLock()
	node := s.nodes[id]
	valid := node != nil && s.config.TelegramEnabled && (node.Profile == nil || !node.Profile.AlertsDisabled) && (s.config.TelegramAlertEpoch == "" || epoch == s.config.TelegramAlertEpoch)
	s.mu.RUnlock()
	if !valid {
		return nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.sdb.db.ExecContext(ctx, `INSERT INTO telegram_alert_state(node_uuid,config_epoch,state_json) VALUES(?,?,?)
		ON CONFLICT(node_uuid) DO UPDATE SET config_epoch=excluded.config_epoch,state_json=excluded.state_json`, id, epoch, string(raw))
	return err
}

func (s *Store) DeleteTelegramAlertState(id string) error {
	return s.DeleteTelegramAlertStateContext(context.Background(), id)
}

func (s *Store) DeleteTelegramAlertStateContext(ctx context.Context, id string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	_, err := s.sdb.db.ExecContext(ctx, "DELETE FROM telegram_alert_state WHERE node_uuid = ?", id)
	return err
}

// A queue worker loads only its node, not every node's alert state.
func (s *Store) LoadTelegramAlertState(epoch, id string) (map[string]TelegramAlertState, error) {
	return s.LoadTelegramAlertStateContext(context.Background(), epoch, id)
}

func (s *Store) LoadTelegramAlertStateContext(ctx context.Context, epoch, id string) (map[string]TelegramAlertState, error) {
	states := make(map[string]TelegramAlertState)
	var raw string
	err := s.sdb.reader().QueryRowContext(ctx, "SELECT state_json FROM telegram_alert_state WHERE node_uuid=? AND config_epoch=?", id, epoch).Scan(&raw)
	if err == sql.ErrNoRows {
		return states, nil
	}
	if err != nil {
		return nil, err
	}
	var state TelegramAlertState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	states[id] = state
	return states, nil
}
