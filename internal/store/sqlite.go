package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"vibemonitor/pkg/protocol"
)

const (
	pingHistoryRetentionSec = 90 * 86400 // 保留 90 天的 Ping 采样数据
)

type sqliteDB struct {
	readDB      *sql.DB
	db          *sql.DB
	tx          *sql.Tx // Set only on a transaction-scoped writer.
	nodeCache   map[string]string
	pingCache   map[string]PingSample
	targetCache map[string]string
}

func (s *sqliteDB) exec(query string, args ...any) (sql.Result, error) {
	if s.tx != nil {
		return s.tx.Exec(query, args...)
	}
	return s.db.Exec(query, args...)
}

func openSQLite(dbPath string) (*sqliteDB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil && filepath.Dir(dbPath) != "." {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Set permissions before SQLite creates WAL/SHM files (they inherit this mode).
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absPath}
	dsn := uri.String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database %s: %w", dbPath, err)
	}

	// SQLite 写锁单连接最安全，避免 database locked
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &sqliteDB{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize sqlite schema: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(dbPath+suffix, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, err
		}
	}

	readDB, err := sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(2)
	readDB.SetMaxIdleConns(2)
	s.readDB = readDB

	return s, nil
}

func (s *sqliteDB) reader() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *sqliteDB) Close() error {
	if s.readDB != nil {
		_ = s.readDB.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *sqliteDB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS site_themes (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		version TEXT NOT NULL,
		description TEXT NOT NULL,
		css TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS site_assets (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		content_type TEXT NOT NULL,
		data BLOB NOT NULL
	);
	CREATE TABLE IF NOT EXISTS config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		admin_username TEXT NOT NULL DEFAULT 'admin',
		admin_password TEXT NOT NULL,
		site_title TEXT NOT NULL DEFAULT 'VibeMonitor',
		site_icon TEXT NOT NULL DEFAULT '',
		auto_discovery_key TEXT NOT NULL DEFAULT '',
		ping_targets_json TEXT NOT NULL DEFAULT '[]',
		telegram_bot_token TEXT NOT NULL DEFAULT '',
		telegram_chat_id TEXT NOT NULL DEFAULT '',
		telegram_enabled INTEGER NOT NULL DEFAULT 0,
		telegram_offline_delay_seconds INTEGER NOT NULL DEFAULT 60,
		telegram_reminder_days INTEGER NOT NULL DEFAULT 7,
		telegram_reminder_hour INTEGER NOT NULL DEFAULT 9,
		telegram_reminder_timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
		telegram_alert_epoch TEXT NOT NULL DEFAULT '',
		telegram_templates_json TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE IF NOT EXISTS telegram_alert_state (
		node_uuid TEXT PRIMARY KEY,
		config_epoch TEXT NOT NULL,
		state_json TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS nodes (
		uuid TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		token TEXT NOT NULL UNIQUE,
		group_name TEXT NOT NULL DEFAULT '',
		region TEXT NOT NULL DEFAULT '',
		online INTEGER NOT NULL DEFAULT 0,
		last_seen INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL DEFAULT 0,
		data_json TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS ping_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		node_uuid TEXT NOT NULL,
		target_name TEXT NOT NULL,
		host TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT '',
		timestamp INTEGER NOT NULL,
		latency INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS resource_history (
		node_uuid TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		cpu_usage REAL,
		ram_usage REAL,
		network_rate INTEGER,
		PRIMARY KEY (node_uuid, timestamp)
	);
	CREATE INDEX IF NOT EXISTS idx_resource_history_time ON resource_history(timestamp);

	CREATE INDEX IF NOT EXISTS idx_ping_lookup ON ping_history(node_uuid, target_name, timestamp);
	CREATE INDEX IF NOT EXISTS idx_ping_cleanup ON ping_history(timestamp);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_ping_unique ON ping_history(node_uuid, target_name, timestamp);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	hasNetworkRate, err := s.hasResourceColumn("network_rate")
	if err != nil {
		return err
	}
	if !hasNetworkRate {
		if _, err := s.db.Exec("ALTER TABLE resource_history ADD COLUMN network_rate INTEGER"); err != nil {
			return err
		}
	}
	// 兼容已有旧测试创建的数据库，确保 data_json 列存在
	_, _ = s.db.Exec("ALTER TABLE nodes ADD COLUMN data_json TEXT DEFAULT ''")
	for _, column := range []struct{ name, fallback string }{{"site_theme", "hex"}, {"color_mode", "light"}, {"telegram_bot_token", ""}, {"telegram_chat_id", ""}} {
		exists, err := s.hasConfigColumn(column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec("ALTER TABLE config ADD COLUMN " + column.name + " TEXT NOT NULL DEFAULT '" + column.fallback + "'"); err != nil {
				return err
			}
		}
	}
	if exists, err := s.hasConfigColumn("telegram_enabled"); err != nil {
		return err
	} else if !exists {
		if _, err := s.db.Exec("ALTER TABLE config ADD COLUMN telegram_enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"telegram_offline_delay_seconds", "INTEGER NOT NULL DEFAULT 60"},
		{"telegram_reminder_days", "INTEGER NOT NULL DEFAULT 7"},
		{"telegram_reminder_hour", "INTEGER NOT NULL DEFAULT 9"},
		{"telegram_reminder_timezone", "TEXT NOT NULL DEFAULT 'Asia/Shanghai'"},
		{"telegram_alert_epoch", "TEXT NOT NULL DEFAULT ''"},
		{"telegram_templates_json", "TEXT NOT NULL DEFAULT '{}'"},
	} {
		exists, err := s.hasConfigColumn(column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec("ALTER TABLE config ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *sqliteDB) hasConfigColumn(name string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM pragma_table_info('config') WHERE name = ?", name).Scan(&count)
	return count > 0, err
}

func (s *sqliteDB) hasResourceColumn(name string) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT count(*) FROM pragma_table_info('resource_history') WHERE name = ?", name).Scan(&count)
	return count > 0, err
}

func (s *sqliteDB) pruneNodePing(nodeUUID string, allowedTargets []protocol.PingTarget) error {
	if len(allowedTargets) == 0 {
		_, err := s.db.Exec("DELETE FROM ping_history WHERE node_uuid = ?", nodeUUID)
		return err
	}
	placeholders := strings.Repeat("?,", len(allowedTargets))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(allowedTargets)+1)
	args = append(args, nodeUUID)
	for _, t := range allowedTargets {
		args = append(args, t.Name)
	}
	_, err := s.db.Exec("DELETE FROM ping_history WHERE node_uuid = ? AND target_name NOT IN ("+placeholders+")", args...)
	return err
}

func (s *sqliteDB) loadConfig() (*Config, error) {
	fields := []string{"admin_username", "admin_password", "site_title", "site_icon", "auto_discovery_key", "ping_targets_json"}
	for _, field := range []struct{ name, fallback string }{
		{"telegram_bot_token", "''"}, {"telegram_chat_id", "''"}, {"telegram_enabled", "0"},
		{"telegram_offline_delay_seconds", "60"}, {"telegram_reminder_days", "7"},
		{"telegram_reminder_hour", "9"}, {"telegram_reminder_timezone", "'Asia/Shanghai'"},
		{"telegram_alert_epoch", "''"},
		{"telegram_templates_json", "'{}'"},
		{"site_theme", "'hex'"},
	} {
		exists, err := s.hasConfigColumn(field.name)
		if err != nil {
			return nil, err
		}
		if exists {
			fields = append(fields, field.name)
		} else {
			fields = append(fields, field.fallback)
		}
	}
	row := s.db.QueryRow("SELECT " + strings.Join(fields, ", ") + " FROM config WHERE id = 1")
	var c Config
	var targetsJSON string
	var templatesJSON string
	err := row.Scan(&c.AdminUsername, &c.AdminPassword, &c.SiteTitle, &c.SiteIcon, &c.AutoDiscoveryKey, &targetsJSON,
		&c.TelegramBotToken, &c.TelegramChatID, &c.TelegramEnabled, &c.TelegramOfflineDelaySeconds,
		&c.TelegramReminderDays, &c.TelegramReminderHour, &c.TelegramReminderTimezone, &c.TelegramAlertEpoch, &templatesJSON, &c.SiteTheme)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 未初始化
		}
		return nil, err
	}
	if targetsJSON != "" {
		if err := json.Unmarshal([]byte(targetsJSON), &c.PingTargets); err != nil {
			return nil, fmt.Errorf("invalid stored ping targets: %w", err)
		}
	}
	if c.PingTargets == nil {
		c.PingTargets = []protocol.PingTarget{}
	}
	if err := json.Unmarshal([]byte(templatesJSON), &c.TelegramTemplates); err != nil {
		return nil, fmt.Errorf("invalid stored Telegram templates: %w", err)
	}
	if c.SiteTheme == "" {
		c.SiteTheme = DefaultTheme
	}
	c.ColorMode = "light"
	return &c, nil
}

func (s *sqliteDB) saveConfig(c *Config) error {
	targetsJSON, err := json.Marshal(c.PingTargets)
	if err != nil {
		return err
	}
	templatesJSON, err := json.Marshal(c.TelegramTemplates)
	if err != nil {
		return err
	}
	query := `
	INSERT INTO config (id, admin_username, admin_password, site_title, site_icon, auto_discovery_key, ping_targets_json, site_theme, color_mode, telegram_bot_token, telegram_chat_id, telegram_enabled, telegram_offline_delay_seconds, telegram_reminder_days, telegram_reminder_hour, telegram_reminder_timezone, telegram_alert_epoch, telegram_templates_json)
	VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		admin_username = excluded.admin_username,
		admin_password = excluded.admin_password,
		site_title = excluded.site_title,
		site_icon = excluded.site_icon,
		auto_discovery_key = excluded.auto_discovery_key,
		ping_targets_json = excluded.ping_targets_json,
		site_theme = excluded.site_theme,
		color_mode = excluded.color_mode,
		telegram_bot_token = excluded.telegram_bot_token,
		telegram_chat_id = excluded.telegram_chat_id,
		telegram_enabled = excluded.telegram_enabled,
		telegram_offline_delay_seconds = excluded.telegram_offline_delay_seconds,
		telegram_reminder_days = excluded.telegram_reminder_days,
		telegram_reminder_hour = excluded.telegram_reminder_hour,
		telegram_reminder_timezone = excluded.telegram_reminder_timezone,
		telegram_alert_epoch = excluded.telegram_alert_epoch,
		telegram_templates_json = excluded.telegram_templates_json;
	`
	_, err = s.exec(query, c.AdminUsername, c.AdminPassword, c.SiteTitle, c.SiteIcon, c.AutoDiscoveryKey, string(targetsJSON), c.SiteTheme, c.ColorMode,
		c.TelegramBotToken, c.TelegramChatID, c.TelegramEnabled, c.TelegramOfflineDelaySeconds, c.TelegramReminderDays,
		c.TelegramReminderHour, c.TelegramReminderTimezone, c.TelegramAlertEpoch, string(templatesJSON))
	return err
}

func (s *sqliteDB) loadNodes() (map[string]*Node, error) {
	rows, err := s.db.Query("SELECT data_json FROM nodes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make(map[string]*Node)
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return nil, err
		}
		var n Node
		if err := json.Unmarshal([]byte(dataJSON), &n); err != nil {
			return nil, err
		}
		if n.PingHistory == nil {
			n.PingHistory = make(map[string][]PingSample)
		}
		nodes[n.UUID] = &n
	}
	return nodes, rows.Err()
}

func (s *sqliteDB) deleteNode(uuid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM nodes WHERE uuid = ?", uuid); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM ping_history WHERE node_uuid = ?", uuid); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM resource_history WHERE node_uuid = ?", uuid); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteDB) saveAllNodes(nodes map[string]*Node) (map[string]string, error) {
	tx := s.tx
	if tx == nil {
		return nil, errors.New("node writes require a transaction")
	}

	// 清理不在当前集合的节点
	if len(nodes) > 0 {
		placeholders := strings.Repeat("?,", len(nodes))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, 0, len(nodes))
		for id := range nodes {
			args = append(args, id)
		}
		if _, err := tx.Exec("DELETE FROM nodes WHERE uuid NOT IN ("+placeholders+")", args...); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec("DELETE FROM nodes"); err != nil {
			return nil, err
		}
	}

	query := `
	INSERT INTO nodes (uuid, name, token, group_name, region, online, last_seen, created_at, data_json)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(uuid) DO UPDATE SET
		name = excluded.name,
		token = excluded.token,
		group_name = excluded.group_name,
		region = excluded.region,
		online = excluded.online,
		last_seen = excluded.last_seen,
		created_at = excluded.created_at,
		data_json = excluded.data_json;
	`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	nextNodes := make(map[string]string, len(nodes))
	for _, n := range nodes {
		clone := *n
		clone.PingHistory = nil
		nodeBytes, err := json.Marshal(&clone)
		if err != nil {
			return nil, err
		}
		nodeJSON := string(nodeBytes)
		nextNodes[n.UUID] = nodeJSON
		if s.nodeCache[n.UUID] == nodeJSON {
			continue
		}
		onlineInt := 0
		if n.Online {
			onlineInt = 1
		}
		_, err = stmt.Exec(n.UUID, n.Name, n.Token, n.Group, n.Region, onlineInt, n.LastSeen.Unix(), n.CreatedAt.Unix(), nodeJSON)
		if err != nil {
			return nil, err
		}
	}
	return nextNodes, nil
}

func (s *sqliteDB) recordPingSample(nodeUUID, targetName, host, method string, timestamp int64, latency int) error {
	query := `INSERT INTO ping_history (node_uuid, target_name, host, method, timestamp, latency) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_uuid, target_name, timestamp) DO UPDATE SET
			latency = excluded.latency,
			host = excluded.host,
			method = excluded.method`
	_, err := s.db.Exec(query, nodeUUID, targetName, host, method, timestamp, latency)
	return err
}

func (s *sqliteDB) getPingHistory(nodeUUID, targetName, host, method string, cutoff int64) ([]PingSample, error) {
	var query string
	var args []any

	if method != "" && host != "" {
		query = "SELECT host, method, timestamp, latency FROM ping_history WHERE node_uuid = ? AND target_name = ? AND host = ? AND method = ? AND timestamp >= ? ORDER BY timestamp ASC"
		args = []any{nodeUUID, targetName, host, method, cutoff}
	} else if host != "" {
		query = "SELECT host, method, timestamp, latency FROM ping_history WHERE node_uuid = ? AND target_name = ? AND host = ? AND timestamp >= ? ORDER BY timestamp ASC"
		args = []any{nodeUUID, targetName, host, cutoff}
	} else {
		query = "SELECT host, method, timestamp, latency FROM ping_history WHERE node_uuid = ? AND target_name = ? AND timestamp >= ? ORDER BY timestamp ASC"
		args = []any{nodeUUID, targetName, cutoff}
	}

	rows, err := s.reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []PingSample
	for rows.Next() {
		var s PingSample
		if err := rows.Scan(&s.Host, &s.Method, &s.Timestamp, &s.Latency); err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	return samples, rows.Err()
}

func (s *sqliteDB) getLatestPingMethod(nodeUUID, targetName, host string) string {
	var method string
	var row *sql.Row
	if host != "" {
		row = s.reader().QueryRow("SELECT method FROM ping_history WHERE node_uuid = ? AND target_name = ? AND host = ? ORDER BY timestamp DESC LIMIT 1", nodeUUID, targetName, host)
	} else {
		row = s.reader().QueryRow("SELECT method FROM ping_history WHERE node_uuid = ? AND target_name = ? ORDER BY timestamp DESC LIMIT 1", nodeUUID, targetName)
	}
	_ = row.Scan(&method)
	return method
}

func (s *sqliteDB) pruneOldPingHistory(beforeTimestamp int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM ping_history WHERE timestamp < ?", beforeTimestamp)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
