package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"vibemonitor/pkg/protocol"
)

// ReadBackup supports complete exports and legacy JSON with a matching sidecar.
func ReadBackup(path string) (*DataFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = ValidateData(raw); err != nil {
		return nil, err
	}
	var data DataFile
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	var sidecar struct {
		Digest string `json:"data_digest"`
		Nodes  map[string]struct {
			History map[string][]PingSample `json:"history"`
			Results []protocol.PingResult   `json:"results"`
		} `json:"nodes"`
	}
	ping, err := os.ReadFile(path + ".ping.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(ping, &sidecar); err != nil {
			return nil, fmt.Errorf("invalid ping sidecar: %w", err)
		}
		digest := sha256.Sum256(raw)
		if sidecar.Digest == hex.EncodeToString(digest[:]) {
			for id, entry := range sidecar.Nodes {
				if n := data.Nodes[id]; n != nil {
					n.PingHistory = entry.History
					if n.LastReport != nil {
						n.LastReport.PingResults = entry.Results
					}
				}
			}
		}
	}
	return &data, nil
}

func prepareImport(data *DataFile) error {
	if !strings.HasPrefix(data.Config.AdminPassword, "$2") {
		hash, err := hashAdminPassword(data.Config.AdminPassword)
		if err != nil {
			return err
		}
		data.Config.AdminPassword = hash
	}
	for _, n := range data.Nodes {
		if n.Profile == nil {
			targets := append([]protocol.PingTarget{}, data.Config.PingTargets...)
			for i := range targets {
				if !strings.Contains(targets[i].Host, ":") {
					targets[i].Host += ":80"
				}
			}
			n.Profile = &NodeProfile{Targets: targets}
		}
		if err := validateProfile(n.Profile); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteDB) migrateLegacy(path string) error {
	if strings.HasSuffix(path, ".db") {
		path = strings.TrimSuffix(path, ".db") + ".json"
	}
	if !strings.HasSuffix(path, ".json") {
		return nil
	}
	config, err := s.loadConfig()
	if err != nil || config != nil {
		return err
	}
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	cleanup, err := migrationCleanupTargets(path)
	if err != nil {
		return fmt.Errorf("migration cleanup preflight: %w", err)
	}
	data, err := ReadBackup(path)
	if err != nil {
		return fmt.Errorf("legacy migration refused; original files retained: %w", err)
	}
	if err = prepareImport(data); err != nil {
		return err
	}
	if err = s.saveSnapshot(data.Config, data.Nodes, true); err != nil {
		return err
	}
	if err = s.verifyMigration(data); err != nil {
		return fmt.Errorf("migration verification failed; original files and backups retained: %w", err)
	}
	if err = cleanup.remove(); err != nil {
		// The committed database remains usable even if filesystem cleanup fails.
		log.Printf("[Store] Migration verified, but cleanup incomplete (manual cleanup required): %v", err)
	}
	return nil
}

// ExportData must be called with the server stopped. It includes all retained history.
func ExportData(dataPath, destination string) error {
	dbPath := resolveDBPath(dataPath)
	outputPath, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	absoluteDB, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	if outputPath == absoluteDB || outputPath == absoluteDB+"-wal" || outputPath == absoluteDB+"-shm" {
		return errors.New("backup output cannot replace database files")
	}
	var data *DataFile
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		data, err = ReadBackup(dataPath)
		if err != nil {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		db, err := openSQLite(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		config, err := db.loadConfig()
		if err != nil {
			return err
		}
		if config == nil {
			return errors.New("database has no configuration")
		}
		nodes, err := db.loadNodes()
		if err != nil {
			return err
		}
		rows, err := db.db.Query("SELECT node_uuid,target_name,host,method,timestamp,latency FROM ping_history ORDER BY timestamp")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, target string
			var sample PingSample
			if err = rows.Scan(&id, &target, &sample.Host, &sample.Method, &sample.Timestamp, &sample.Latency); err != nil {
				return err
			}
			if n := nodes[id]; n != nil {
				n.PingHistory[target] = append(n.PingHistory[target], sample)
			}
		}
		if err = rows.Err(); err != nil {
			return err
		}
		data = &DataFile{Config: *config, Nodes: nodes}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err = ValidateData(raw); err != nil {
		return err
	}
	return writeAtomic(destination, raw)
}

// RestoreData replaces the complete database in one transaction. Stop the server first.
func RestoreData(source, dataPath string) error {
	data, err := ReadBackup(source)
	if err != nil {
		return err
	}
	if err = prepareImport(data); err != nil {
		return err
	}
	db, err := openSQLite(resolveDBPath(dataPath))
	if err != nil {
		return err
	}
	defer db.Close()
	return db.saveSnapshot(data.Config, data.Nodes, true)
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
