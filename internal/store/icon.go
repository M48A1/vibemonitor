package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

var (
	svgScriptTag = regexp.MustCompile(`(?i)<\s*script\b`)
	svgEventAttr = regexp.MustCompile(`(?i)\bon[a-z]{2,}\s*=`)
	svgJSURL     = regexp.MustCompile(`(?i)javascript:`)
)

// validIconContent rejects SVG payloads that could execute scripts if opened
// directly. Served as an <img> behind the site's Content-Security-Policy the
// risk is already low; this is defense in depth.
func validIconContent(data []byte, kind string) bool {
	if kind != "image/svg+xml" {
		return true
	}
	return !svgScriptTag.Match(data) && !svgEventAttr.Match(data) && !svgJSURL.Match(data)
}

// SaveSiteIcon stores the bytes and their URL in the same transaction.
// Empty data deletes the uploaded icon and clears its URL.
func (s *Store) SaveSiteIcon(data []byte, kind string) (string, error) {
	return s.SaveSiteIconContext(context.Background(), data, kind)
}

func (s *Store) SaveSiteIconContext(ctx context.Context, data []byte, kind string) (string, error) {
	if len(data) > MaxIconBytes || (len(data) > 0 && (!validIconType(kind) || !validIconContent(data, kind))) {
		return "", errors.New("invalid icon type, size, or content")
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.RLock()
	next := s.config
	next.SiteIcon = ""
	if len(data) > 0 {
		next.SiteIcon = "/api/site-icon?v=" + GenerateToken(8)
	}
	s.mu.RUnlock()
	tx, err := s.sdb.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM site_assets"); err != nil {
		return "", err
	}
	if len(data) > 0 {
		if _, err = tx.ExecContext(ctx, "INSERT INTO site_assets(id, content_type, data) VALUES(1, ?, ?)", kind, data); err != nil {
			return "", err
		}
	}
	writer := &sqliteDB{tx: tx}
	if err = writer.saveConfigContext(ctx, &next); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.config = next
	s.notifyUpdate()
	s.mu.Unlock()
	return next.SiteIcon, nil
}

func (s *Store) SiteIconData() ([]byte, string, error) {
	return s.SiteIconDataContext(context.Background())
}

func (s *Store) SiteIconDataContext(ctx context.Context) ([]byte, string, error) {
	var data []byte
	var kind string
	err := s.sdb.reader().QueryRowContext(ctx, "SELECT data, content_type FROM site_assets WHERE id=1").Scan(&data, &kind)
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
