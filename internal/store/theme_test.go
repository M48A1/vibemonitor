package store

import (
	"path/filepath"
	"testing"
)

func TestLegacyAppearanceUsesHexAfterRestartAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.db")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	theme, mode := "hex", "light"
	if err := s.UpdateSettings("", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.sdb.db.Exec(`UPDATE config SET site_theme = 'default', color_mode = 'dark' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if c := s.GetConfig(); c.SiteTheme != theme || c.ColorMode != mode {
		t.Fatalf("appearance lost on restart: %+v", c)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := ExportData(path, backup); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored.db")
	if err := RestoreData(backup, dest); err != nil {
		t.Fatal(err)
	}
	restored, err := New(dest, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if c := restored.GetConfig(); c.SiteTheme != theme || c.ColorMode != mode {
		t.Fatal("backup lost appearance")
	}
	// A failed disk write must not publish changed settings in memory.
	if _, err := restored.sdb.db.Exec(`CREATE TRIGGER reject_theme BEFORE UPDATE ON config BEGIN SELECT RAISE(FAIL, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := restored.UpdateSettings("changed", nil, ""); err == nil {
		t.Fatal("disk failure accepted")
	}
	if c := restored.GetConfig(); c.SiteTheme != "hex" || c.SiteTitle == "changed" {
		t.Fatal("failed write changed memory")
	}
}

func TestLegacyAppearanceMigrationAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"site_theme", "color_mode"} {
		if _, err := db.db.Exec("ALTER TABLE config DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	// Old backups remain valid without modifying them.
	if err := ValidateBackup(path); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored.db")
	if err := RestoreData(path, dest); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, dest} {
		upgraded, err := New(file, "")
		if err != nil {
			t.Fatal(err)
		}
		if c := upgraded.GetConfig(); c.SiteTheme != "hex" || c.ColorMode != "light" {
			t.Fatal("legacy database did not upgrade to Hex")
		}
		if err := upgraded.UpdateSettings("", nil, ""); err != nil {
			t.Fatal(err)
		}
		upgraded.Close()
	}
}
