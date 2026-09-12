package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxIconBytes = 2 << 20

func validIconType(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon":
		return true
	}
	return false
}

// SaveSiteIcon stores the bytes and their URL in the same transaction.
// Empty data deletes the uploaded icon and clears its URL.
func (s *Store) SaveSiteIcon(data []byte, kind string) (string, error) {
	if len(data) > MaxIconBytes || (len(data) > 0 && !validIconType(kind)) {
		return "", errors.New("invalid icon type or size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	next.SiteIcon = ""
	if len(data) > 0 {
		next.SiteIcon = "/api/site-icon?v=" + GenerateToken(8)
	}
	tx, err := s.sdb.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM site_assets"); err != nil {
		return "", err
	}
	if len(data) > 0 {
		if _, err = tx.Exec("INSERT INTO site_assets(id, content_type, data) VALUES(1, ?, ?)", kind, data); err != nil {
			return "", err
		}
	}
	writer := &sqliteDB{tx: tx}
	if err = writer.saveConfig(&next); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.config = next
	s.notifyUpdate()
	return next.SiteIcon, nil
}

func (s *Store) SiteIconData() ([]byte, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var data []byte
	var kind string
	err := s.sdb.db.QueryRow("SELECT data, content_type FROM site_assets WHERE id=1").Scan(&data, &kind)
	return data, kind, err
}

// Import only the old uploaded icon referenced by this database. Originals are
// retained, but are never served after import or used to resurrect deleted icons.
func (s *Store) importLegacyIcon() error {
	if !strings.HasPrefix(s.config.SiteIcon, "/api/site-icon") {
		return nil
	}
	_, _, err := s.SiteIconData()
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, item := range []struct{ ext, kind string }{
		{"png", "image/png"}, {"jpg", "image/jpeg"}, {"jpeg", "image/jpeg"},
		{"gif", "image/gif"}, {"webp", "image/webp"}, {"svg", "image/svg+xml"}, {"ico", "image/x-icon"},
	} {
		path := filepath.Join(filepath.Dir(s.dbPath), "site-icon."+item.ext)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxIconBytes {
			return fmt.Errorf("invalid legacy icon: %s", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxIconBytes+1))
		f.Close()
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("empty legacy icon: %s", path)
		}
		_, err = s.SaveSiteIcon(data, item.kind)
		return err
	}
	return nil
}
