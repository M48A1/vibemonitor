package store

import (
	"errors"
	"path/filepath"
	"strings"
)

func validateDatabasePath(path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".db", ".sqlite", ".sqlite3":
		return nil
	default:
		return errors.New("data path must end in .db, .sqlite or .sqlite3; update the service --data argument to the existing SQLite database")
	}
}
