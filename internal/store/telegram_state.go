package store

import (
	"encoding/json"
	"fmt"
)

// TelegramAlertState records transitions that have already been observed or delivered.
type TelegramAlertState struct {
	Seen                bool   `json:"seen"`
	Online              bool   `json:"online"`
	OfflineAlerted      bool   `json:"offline_alerted"`
	CPU                 bool   `json:"cpu"`
	Memory              bool   `json:"memory"`
	Traffic             bool   `json:"traffic"`
	TrafficWarningCycle string `json:"traffic_warning_cycle,omitempty"`
	ReminderDate        string `json:"reminder_date,omitempty"`
	ReminderFor         string `json:"reminder_for,omitempty"`
}

func (s *Store) LoadTelegramAlertStates(epoch string) (map[string]TelegramAlertState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.sdb.db.Query("SELECT node_uuid, state_json FROM telegram_alert_state WHERE config_epoch = ?", epoch)
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
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err = s.sdb.db.Exec(`INSERT INTO telegram_alert_state(node_uuid,config_epoch,state_json) VALUES(?,?,?)
		ON CONFLICT(node_uuid) DO UPDATE SET config_epoch=excluded.config_epoch,state_json=excluded.state_json`, id, epoch, string(raw))
	return err
}

func (s *Store) DeleteTelegramAlertState(id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := s.sdb.db.Exec("DELETE FROM telegram_alert_state WHERE node_uuid = ?", id)
	return err
}
