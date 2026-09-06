package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"vibemonitor/pkg/protocol"
)

const (
	pingHistoryRetentionSec = 7 * 86400 // 保留 7 天的 Ping 采样数据
)

type pingFile struct {
	DataDigest string              `json:"data_digest"`
	Nodes      map[string]pingNode `json:"nodes"`
}
type pingNode struct {
	History map[string][]PingSample `json:"history,omitempty"`
	Results []protocol.PingResult   `json:"results,omitempty"`
}

type sqliteDB struct {
	db *sql.DB
}

func openSQLite(dbPath string) (*sqliteDB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil && filepath.Dir(dbPath) != "." {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
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

	return s, nil
}

func (s *sqliteDB) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *sqliteDB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS config (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		admin_username TEXT NOT NULL DEFAULT 'admin',
		admin_password TEXT NOT NULL,
		site_title TEXT NOT NULL DEFAULT 'VibeMonitor',
		announcement TEXT NOT NULL DEFAULT '',
		site_icon TEXT NOT NULL DEFAULT '',
		auto_discovery_key TEXT NOT NULL DEFAULT '',
		ping_targets_json TEXT NOT NULL DEFAULT '[]'
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

	CREATE INDEX IF NOT EXISTS idx_ping_lookup ON ping_history(node_uuid, target_name, timestamp);
	CREATE INDEX IF NOT EXISTS idx_ping_cleanup ON ping_history(timestamp);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_ping_unique ON ping_history(node_uuid, target_name, timestamp);
	`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	// 兼容已有旧测试创建的数据库，确保 data_json 列存在
	_, _ = s.db.Exec("ALTER TABLE nodes ADD COLUMN data_json TEXT DEFAULT ''")
	return nil
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
	row := s.db.QueryRow("SELECT admin_username, admin_password, site_title, announcement, site_icon, auto_discovery_key, ping_targets_json FROM config WHERE id = 1")
	var c Config
	var targetsJSON string
	err := row.Scan(&c.AdminUsername, &c.AdminPassword, &c.SiteTitle, &c.Announcement, &c.SiteIcon, &c.AutoDiscoveryKey, &targetsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 未初始化
		}
		return nil, err
	}
	if targetsJSON != "" {
		_ = json.Unmarshal([]byte(targetsJSON), &c.PingTargets)
	}
	if c.PingTargets == nil {
		c.PingTargets = []protocol.PingTarget{}
	}
	return &c, nil
}

func (s *sqliteDB) saveConfig(c *Config) error {
	targetsJSON, err := json.Marshal(c.PingTargets)
	if err != nil {
		return err
	}
	query := `
	INSERT INTO config (id, admin_username, admin_password, site_title, announcement, site_icon, auto_discovery_key, ping_targets_json)
	VALUES (1, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		admin_username = excluded.admin_username,
		admin_password = excluded.admin_password,
		site_title = excluded.site_title,
		announcement = excluded.announcement,
		site_icon = excluded.site_icon,
		auto_discovery_key = excluded.auto_discovery_key,
		ping_targets_json = excluded.ping_targets_json;
	`
	_, err = s.db.Exec(query, c.AdminUsername, c.AdminPassword, c.SiteTitle, c.Announcement, c.SiteIcon, c.AutoDiscoveryKey, string(targetsJSON))
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
	return tx.Commit()
}

func (s *sqliteDB) saveAllNodes(nodes map[string]*Node) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 清理不在当前集合的节点
	if len(nodes) > 0 {
		placeholders := strings.Repeat("?,", len(nodes))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, 0, len(nodes))
		for id := range nodes {
			args = append(args, id)
		}
		if _, err := tx.Exec("DELETE FROM nodes WHERE uuid NOT IN ("+placeholders+")", args...); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec("DELETE FROM nodes"); err != nil {
			return err
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
		return err
	}
	defer stmt.Close()

	for _, n := range nodes {
		clone := *n
		clone.PingHistory = nil
		nodeBytes, err := json.Marshal(&clone)
		if err != nil {
			return err
		}
		onlineInt := 0
		if n.Online {
			onlineInt = 1
		}
		_, err = stmt.Exec(n.UUID, n.Name, n.Token, n.Group, n.Region, onlineInt, n.LastSeen.Unix(), n.CreatedAt.Unix(), string(nodeBytes))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
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

	rows, err := s.db.Query(query, args...)
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
		row = s.db.QueryRow("SELECT method FROM ping_history WHERE node_uuid = ? AND target_name = ? AND host = ? ORDER BY timestamp DESC LIMIT 1", nodeUUID, targetName, host)
	} else {
		row = s.db.QueryRow("SELECT method FROM ping_history WHERE node_uuid = ? AND target_name = ? ORDER BY timestamp DESC LIMIT 1", nodeUUID, targetName)
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

// migrateFromJSON 从旧的 JSON 数据文件中迁移数据到 SQLite
func (s *sqliteDB) migrateFromJSON(jsonPath string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}

	var df DataFile
	if err := json.Unmarshal(data, &df); err != nil {
		return fmt.Errorf("failed to parse json for migration: %w", err)
	}

	log.Printf("[Store Migration] Detected legacy json storage: %s. Migrating to SQLite...", jsonPath)

	if err := s.saveConfig(&df.Config); err != nil {
		return fmt.Errorf("failed to migrate config: %w", err)
	}

	if err := s.saveAllNodes(df.Nodes); err != nil {
		return fmt.Errorf("failed to migrate nodes: %w", err)
	}

	// 检查是否有 sidecar ping 文件
	pingPath := jsonPath + ".ping.json"
	if pingData, err := os.ReadFile(pingPath); err == nil {
		var pf pingFile
		if json.Unmarshal(pingData, &pf) == nil {
			tx, err := s.db.Begin()
			if err == nil {
				stmt, err := tx.Prepare("INSERT INTO ping_history (node_uuid, target_name, host, method, timestamp, latency) VALUES (?, ?, ?, ?, ?, ?)")
				if err == nil {
					count := 0
					for nodeUUID, entry := range pf.Nodes {
						for targetName, samples := range entry.History {
							for _, smp := range samples {
								_, _ = stmt.Exec(nodeUUID, targetName, smp.Host, smp.Method, smp.Timestamp, smp.Latency)
								count++
							}
						}
					}
					_ = stmt.Close()
					_ = tx.Commit()
					log.Printf("[Store Migration] Migrated %d ping samples from sidecar %s", count, pingPath)
				}
			}
		}
	}

	// 迁移完成，重命名旧文件为 .bak
	_ = os.Rename(jsonPath, jsonPath+".bak")
	_ = os.Rename(pingPath, pingPath+".bak")
	log.Printf("[Store Migration] Migration to SQLite completed successfully! Original files backed up as .bak")
	return nil
}
