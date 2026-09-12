package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Verify committed data, not just the in-memory store, before removing sources.
func (s *sqliteDB) verifyMigration(expected *DataFile) error {
	var integrity string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity check: %s", integrity)
	}
	config, err := s.loadConfig()
	if err != nil {
		return err
	}
	want, err := json.Marshal(expected.Config)
	if err != nil {
		return err
	}
	// loadConfig normalizes a nil target list to an empty list.
	if expected.Config.PingTargets == nil && config != nil {
		config.PingTargets = nil
	}
	got, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if string(want) != string(got) {
		return errors.New("configuration mismatch")
	}
	nodes, err := s.loadNodes()
	if err != nil {
		return err
	}
	if len(nodes) != len(expected.Nodes) {
		return errors.New("node count mismatch")
	}
	for id, n := range expected.Nodes {
		actual := nodes[id]
		if actual == nil {
			return fmt.Errorf("missing node %s", id)
		}
		copy := *n
		copy.PingHistory, actual.PingHistory = nil, nil
		want, err := json.Marshal(copy)
		if err != nil {
			return err
		}
		got, err := json.Marshal(actual)
		if err != nil {
			return err
		}
		if string(want) != string(got) {
			return fmt.Errorf("node %s mismatch", id)
		}
	}
	// saveSnapshot records exactly the configured, provenance-checked samples.
	rows, err := s.db.Query("SELECT node_uuid,target_name,host,method,timestamp,latency FROM ping_history")
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, target string
		var sample PingSample
		if err := rows.Scan(&id, &target, &sample.Host, &sample.Method, &sample.Timestamp, &sample.Latency); err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%s/%d", id, target, sample.Timestamp)
		if expected, ok := s.pingCache[key]; !ok || expected != sample {
			return errors.New("ping sample mismatch")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(s.pingCache) {
		return errors.New("ping sample count mismatch")
	}
	return nil
}

type migrationCleanupTarget struct {
	path   string
	info   os.FileInfo
	backup bool
}
type migrationCleanup []migrationCleanupTarget

func migrationCleanupTargets(source string) (migrationCleanup, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	// Resolve the parent once; never traverse a symlink at a deletion target.
	dir, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	source = filepath.Join(dir, filepath.Base(absolute))
	// A database symlink could point inside the backup directory being removed.
	if info, err := os.Lstat(resolveDBPath(source)); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("migration database must be a regular file, not a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	targets := []string{source, source + ".ping.json", filepath.Join(dir, "backups")}
	var result migrationCleanup
	for i, path := range targets {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing symlink cleanup target %s", path)
		}
		if (i == 2 && !info.IsDir()) || (i != 2 && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("unexpected cleanup target type: %s", path)
		}
		result = append(result, migrationCleanupTarget{path: path, info: info, backup: i == 2})
	}
	return result, nil
}

func (targets migrationCleanup) remove() error {
	// Check all identities before deleting anything if paths changed during import.
	for _, target := range targets {
		current, err := os.Lstat(target.path)
		if err != nil {
			return err
		}
		if !os.SameFile(target.info, current) || current.Mode()&os.ModeSymlink != 0 || !target.info.ModTime().Equal(current.ModTime()) || target.info.Size() != current.Size() {
			return fmt.Errorf("cleanup target changed: %s", target.path)
		}
	}
	for _, target := range targets {
		var err error
		if target.backup {
			err = os.RemoveAll(target.path)
		} else {
			err = os.Remove(target.path)
		}
		if err != nil {
			return fmt.Errorf("remove %s: %w", target.path, err)
		}
		log.Printf("[Store] Migration verified; deleted legacy data/backup: %s (no automatic recovery copy retained)", target.path)
	}
	return nil
}
