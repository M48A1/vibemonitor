package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestSQLiteBackupRestoreWithWALHistory(t *testing.T) {
	s, n, path := pingStore(t)
	profile := &NodeProfile{Targets: []protocol.PingTarget{{Name: "target", Host: "192.0.2.1:80"}}, Price: 12, Currency: "USD", DueDate: "2027-01-01", PaymentCycle: "month"}
	if err := s.UpdateNodeWithOptions(n.UUID, NodeOptions{Profile: profile}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := 0; i < 50; i++ {
		if err := s.sdb.recordPingSample(n.UUID, "target", "192.0.2.1:80", "tcp", now-int64(i)*3600, i); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the source open so the export must include committed WAL content.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatal("expected live WAL", err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := ExportData(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBackup(backup); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(backup)
	if err != nil || len(raw) < 16 || string(raw[:16]) != "SQLite format 3\x00" {
		t.Fatal("backup is not SQLite", err)
	}
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("incorrect backup permissions", err)
	}
	// Moving only the backup file to another directory must be sufficient.
	portable := filepath.Join(t.TempDir(), "portable.db")
	if err := os.WriteFile(portable, raw, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	old, err := New(destination, "different")
	if err != nil {
		t.Fatal(err)
	}
	oldNode, err := old.CreateNode("remove me", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreData(portable, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := New(destination, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if !restored.VerifyAdminPassword("pass") || restored.FindNodeByToken(n.Token) == nil || restored.GetNode(oldNode.UUID) != nil {
		t.Fatal("credentials or nodes not restored")
	}
	if restored.GetNode(n.UUID).Profile.Price != 12 {
		t.Fatal("billing not restored")
	}
	h, err := restored.GetPingHistory(n.UUID, "target", "all")
	if err != nil || len(h.Samples) != 50 {
		t.Fatalf("complete history not restored: %+v %v", h, err)
	}
}

func TestSQLiteRestoreFailureRollsBack(t *testing.T) {
	s, n, path := pingStore(t)
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_restore BEFORE INSERT ON config BEGIN SELECT RAISE(ABORT,'restore failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.db")
	replacement, err := New(source, "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreData(source, path); err == nil {
		t.Fatal("expected restore failure")
	}
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodes, err := db.loadNodes()
	if err != nil || nodes[n.UUID] == nil {
		t.Fatal("failed restore deleted original nodes", err)
	}
	cfg, err := db.loadConfig()
	if err != nil || cfg.AdminPassword == "" {
		t.Fatal("failed restore lost configuration", err)
	}
}

func TestSQLiteOnlyDoesNotImportOrDeleteLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "data.json")
	if err := os.WriteFile(legacy, []byte(`{"config":{"admin_password":"old"},"nodes":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := New(legacy, "new"); err == nil {
		s.Close()
		t.Fatal("legacy data path accepted")
	}
	if err := ValidateBackup(legacy); err == nil {
		t.Fatal("JSON backup accepted")
	}
	path := filepath.Join(dir, "data.db")
	s, err := New(path, "new")
	if err != nil {
		t.Fatal(err)
	}
	if !s.VerifyAdminPassword("new") {
		t.Fatal("legacy password imported")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreData(legacy, path); err == nil {
		t.Fatal("JSON restore accepted")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("legacy file deleted", err)
	}
}

func TestSQLiteBackupRejectsUnsafeDestinations(t *testing.T) {
	s, _, path := pingStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{path, path + "-wal", path + "-shm"} {
		if err := ExportData(path, output); err == nil {
			t.Fatal("database file overwritten", output)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if err := ExportData(path, alias); err == nil {
		t.Fatal("database symlink overwritten")
	}
	if err := RestoreData(path, alias); err == nil {
		t.Fatal("restore onto source alias accepted")
	}
	output := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(output, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ExportData(path, output); err == nil {
		t.Fatal("nonempty backup overwritten")
	}
	if raw, err := os.ReadFile(output); err != nil || string(raw) != "keep" {
		t.Fatal("failed export changed output", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := ValidateBackup(missing); err == nil {
		t.Fatal("missing backup accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("validation created a database")
	}
}
