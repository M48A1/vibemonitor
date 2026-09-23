package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// These explicit columns are the supported on-disk backup format.
var backupTables = []struct{ name, columns string }{
	{"config", "id,admin_username,admin_password,site_title,site_icon,auto_discovery_key,ping_targets_json"},
	{"nodes", "uuid,name,token,group_name,region,online,last_seen,created_at,data_json"},
	{"ping_history", "id,node_uuid,target_name,host,method,timestamp,latency"},
}

func validateDatabasePath(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".db", ".sqlite", ".sqlite3":
		return nil
	default:
		return errors.New("data path must end in .db, .sqlite or .sqlite3; update the service --data argument to the existing SQLite database")
	}
}

func sqliteReadURI(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	return uri.String(), nil
}

func openBackup(path string) (*sqliteDB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var header [16]byte
	_, readErr := io.ReadFull(f, header[:])
	closeErr := f.Close()
	if readErr != nil || string(header[:]) != "SQLite format 3\x00" {
		return nil, errors.New("backup must be a SQLite database")
	}
	if closeErr != nil {
		return nil, closeErr
	}
	uri, err := sqliteReadURI(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", uri+"&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &sqliteDB{db: db}, nil
}

// ValidateBackup reads a SQLite backup without creating or migrating any schema.
func ValidateBackup(path string) error {
	db, err := openBackup(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity check: %s", integrity)
	}
	for _, table := range backupTables {
		var kind string
		if err := db.db.QueryRow("SELECT type FROM sqlite_schema WHERE name=?", table.name).Scan(&kind); err != nil {
			return err
		}
		if kind != "table" {
			return fmt.Errorf("%s is not a backup table", table.name)
		}
		rows, err := db.db.Query("SELECT " + table.columns + " FROM " + table.name + " LIMIT 0")
		if err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	// v1.0.41 and earlier SQLite backups have no embedded icon table.
	var assetTables int
	if err := db.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name='site_assets' AND type='table'").Scan(&assetTables); err != nil {
		return err
	}
	if assetTables > 0 {
		rows, err := db.db.Query("SELECT id, content_type, length(data) FROM site_assets")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, size int
			var kind string
			if err := rows.Scan(&id, &kind, &size); err != nil {
				return err
			}
			if id != 1 || size <= 0 || size > MaxIconBytes || !validIconType(kind) {
				return errors.New("invalid embedded site icon")
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	config, err := db.loadConfig()
	if err != nil {
		return err
	}
	if config == nil || config.AdminUsername == "" {
		return errors.New("backup has no administrator configuration")
	}
	if _, err := bcrypt.Cost([]byte(config.AdminPassword)); err != nil {
		return errors.New("backup has no valid password hash")
	}
	if err := validatePingTargets(config.PingTargets); err != nil {
		return err
	}
	nodes, err := db.loadNodes()
	if err != nil {
		return err
	}
	var count int
	if err := db.db.QueryRow("SELECT count(*) FROM nodes").Scan(&count); err != nil {
		return err
	}
	if count != len(nodes) {
		return errors.New("backup has duplicate node identities")
	}
	tokens := make(map[string]bool, len(nodes))
	for id, node := range nodes {
		if id == "" || node.Token == "" || tokens[node.Token] {
			return errors.New("invalid node identity in backup")
		}
		tokens[node.Token] = true
		if err := validateProfile(node.Profile); err != nil {
			return err
		}
	}
	return nil
}

func rejectSameDatabase(source, destination string) error {
	output, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	outputInfo, statErr := os.Stat(output)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		input, err := filepath.Abs(source + suffix)
		if err != nil {
			return err
		}
		if input == output {
			return errors.New("source and destination database files must differ")
		}
		if inputInfo, err := os.Stat(input); err == nil && outputInfo != nil && os.SameFile(inputInfo, outputInfo) {
			return errors.New("source and destination refer to the same database file")
		}
	}
	return nil
}

// ExportData uses SQLite's consistent snapshot, including committed WAL data.
// Stop the server first if its pending in-memory metrics must also be included.
func ExportData(source, destination string) error {
	if err := rejectSameDatabase(source, destination); err != nil {
		return err
	}
	if err := ValidateBackup(source); err != nil {
		return err
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return errors.New("backup output must be absent or an empty regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".sqlite-backup-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err := f.Close(); err != nil {
		return err
	}
	db, err := openBackup(source)
	if err != nil {
		return err
	}
	_, vacuumErr := db.db.Exec("VACUUM INTO ?", temp)
	closeErr := db.Close()
	if vacuumErr != nil {
		return vacuumErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ValidateBackup(temp); err != nil {
		return err
	}
	return os.Rename(temp, destination)
}

// RestoreData copies supported tables directly in one SQLite transaction.
// The server must be stopped so its in-memory state cannot overwrite the restore.
func RestoreData(source, destination string) error {
	if err := validateDatabasePath(destination); err != nil {
		return err
	}
	if err := rejectSameDatabase(source, destination); err != nil {
		return err
	}
	// Freeze the source, including any WAL, before touching the destination.
	tempDir, err := os.MkdirTemp("", "vibemonitor-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	snapshot := filepath.Join(tempDir, "snapshot.db")
	if err := ExportData(source, snapshot); err != nil {
		return err
	}
	sourceDB, err := openBackup(snapshot)
	if err != nil {
		return err
	}
	config, err := sourceDB.loadConfig()
	closeErr := sourceDB.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	db, err := openSQLite(destination)
	if err != nil {
		return err
	}
	defer db.Close()
	uri, err := sqliteReadURI(snapshot)
	if err != nil {
		return err
	}
	if _, err := db.db.Exec("ATTACH DATABASE ? AS restore_source", uri); err != nil {
		return err
	}
	defer db.db.Exec("DETACH DATABASE restore_source")
	tx, err := db.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM main.site_assets"); err != nil {
		return err
	}
	var assetTables int
	if err := tx.QueryRow("SELECT count(*) FROM restore_source.sqlite_schema WHERE name='site_assets' AND type='table'").Scan(&assetTables); err != nil {
		return err
	}
	if assetTables > 0 {
		if _, err := tx.Exec("INSERT INTO main.site_assets(id, content_type, data) SELECT id, content_type, data FROM restore_source.site_assets"); err != nil {
			return err
		}
	}
	for i := len(backupTables) - 1; i >= 0; i-- {
		if _, err := tx.Exec("DELETE FROM " + backupTables[i].name); err != nil {
			return err
		}
	}
	for _, table := range backupTables {
		query := "INSERT INTO main." + table.name + " (" + table.columns + ") SELECT " + table.columns + " FROM restore_source." + table.name
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE main.config SET site_theme = ?, color_mode = ? WHERE id = 1", config.SiteTheme, config.ColorMode); err != nil {
		return err
	}
	return tx.Commit()
}
