package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vibemonitor/pkg/protocol"
)

func TestIconBackupAndLegacySQLiteRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data := []byte("test icon bytes")
	url, err := s.SaveSiteIcon(data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := ExportData(path, backup); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := RestoreData(backup, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := New(destination, "")
	if err != nil {
		t.Fatal(err)
	}
	got, kind, err := restored.SiteIconData()
	if err != nil || !bytes.Equal(got, data) || kind != "image/png" || restored.GetConfig().SiteIcon != url {
		t.Fatalf("icon not restored: %s %s %v", got, kind, err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a v1.0.41 backup with no asset table; stale destination assets must disappear.
	old, err := openSQLite(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec("DROP TABLE site_assets"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if err := ValidateBackup(backup); err != nil {
		t.Fatal(err)
	}
	if err := RestoreData(backup, destination); err != nil {
		t.Fatal(err)
	}
	restored, err = New(destination, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, _, err := restored.SiteIconData(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale icon survived restore: %v", err)
	}
}

func TestLegacyIconImportAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSiteIcon("/api/site-icon?t=old"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	icon := filepath.Join(filepath.Dir(path), "site-icon.png")
	if err := os.WriteFile(icon, []byte("legacy icon"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := s.SiteIconData()
	if err != nil || string(data) != "legacy icon" {
		t.Fatal("legacy icon not imported", err)
	}
	if _, err := s.SaveSiteIcon(nil, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.SiteIconData(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted icon resurrected", err)
	}
	if _, err := os.Stat(icon); err != nil {
		t.Fatal("original should be preserved", err)
	}
}

func TestIconAndSettingsRollback(t *testing.T) {
	s, _, _ := pingStore(t)
	url, err := s.SaveSiteIcon([]byte("original"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_config BEFORE UPDATE ON config BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSiteIcon([]byte("replacement"), "image/png"); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := s.SaveSiteIcon(nil, ""); err == nil {
		t.Fatal("expected delete failure")
	}
	data, _, err := s.SiteIconData()
	if err != nil || string(data) != "original" || s.GetConfig().SiteIcon != url {
		t.Fatal("failed save destroyed icon", err)
	}
	newIcon := "https://example.com/icon.png"
	if err := s.UpdateSettingsWithIcon("replacement", nil, "new-pass", &newIcon); err == nil {
		t.Fatal("expected settings failure")
	}
	if !s.VerifyAdminPassword("pass") || s.GetConfig().SiteIcon != url {
		t.Fatal("failed settings changed credentials or icon")
	}
}

func TestTrafficSourceChangePreservesUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "test", ResetDay: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		source      string
		total, want int64
	}{
		{"", 100, 0}, {"", 200, 200},
		{"interfaces-v1:eth0", 50, 200}, {"interfaces-v1:eth0", 60, 220},
		{"interfaces-v1:ens3", 9000, 220}, {"interfaces-v1:ens3", 9010, 240},
	} {
		_, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{Source: sample.source, TotalUp: sample.total, TotalDown: sample.total}}, "")
		if err != nil || s.GetNode(n.UUID).CurrentCycleUsed != sample.want {
			t.Fatalf("sample %+v failed: %v", sample, err)
		}
	}
	s.Close()
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{Source: "interfaces-v1:ens3", TotalUp: 9020, TotalDown: 9020}}, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 260 {
		t.Fatalf("restart lost baseline: %d", got)
	}
}
