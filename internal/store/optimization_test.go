package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestHashedPasswordCannotBeUsedAsPassword(t *testing.T) {
	s, _, _ := pingStore(t)
	hash := s.GetConfig().AdminPassword
	if s.VerifyAdminPassword(hash) {
		t.Fatal("stored hash accepted as password")
	}
	if !s.VerifyAdminPassword("pass") {
		t.Fatal("original password rejected")
	}
	s.mu.Lock()
	s.config.AdminPassword = "legacy"
	s.mu.Unlock()
	if !s.VerifyAdminPassword("legacy") || s.GetConfig().AdminPassword == "legacy" {
		t.Fatal("legacy password not migrated")
	}
}

func TestBillingWithoutTargetsSurvivesRestart(t *testing.T) {
	s, n, path := pingStore(t)
	profile := &NodeProfile{Targets: []protocol.PingTarget{}, Price: 45, Currency: "EUR", PaymentCycle: "year", DueDate: "2027-01-31"}
	if err := s.UpdateNodeWithOptions(n.UUID, NodeOptions{Profile: profile}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.GetNode(n.UUID).Profile
	if got.Price != 45 || got.DueDate != profile.DueDate || got.Currency != "EUR" || got.PaymentCycle != "year" || len(got.Targets) != 0 {
		t.Fatalf("profile changed: %+v", got)
	}
}

func TestLegacyMigrationCleansSourceAndPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	target := protocol.PingTarget{Name: "target", Host: "example.com:443"}
	data := DataFile{Config: Config{AdminUsername: "owner", AdminPassword: "legacy", SiteTitle: "old", PingTargets: []protocol.PingTarget{}}, Nodes: map[string]*Node{
		"node": {UUID: "node", Token: "secret", CurrentCycleUsed: 12345, Profile: &NodeProfile{Targets: []protocol.PingTarget{target}, Price: 9}},
	}}
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	ping, _ := json.Marshal(map[string]any{"data_digest": hex.EncodeToString(digest[:]), "nodes": map[string]any{"node": map[string]any{"history": map[string][]PingSample{"target": {{Host: target.Host, Method: "tcp", Timestamp: time.Now().Unix() - 60, Latency: 42}}}}}})
	if err := os.WriteFile(path+".ping.json", ping, 0600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(filepath.Join(backups, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old.json", "data-without-extension", "nested/old.ping.json"} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("old backup"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(filepath.Dir(path), "unrelated.json")
	if err := os.WriteFile(unrelated, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep.json")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(backups, "external")); err != nil {
		t.Fatal(err)
	}
	s, err := New(path, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if !s.VerifyAdmin("owner", "legacy") || s.GetNode("node").CurrentCycleUsed != 12345 {
		t.Fatal("migration lost credentials or traffic")
	}
	h, err := s.GetPingHistory("node", "target", "all")
	if err != nil || len(h.Samples) != 1 || h.Samples[0].Latency != 42 {
		t.Fatalf("history lost: %+v %v", h, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, deleted := range []string{path, path + ".ping.json", backups} {
		if _, err := os.Lstat(deleted); !os.IsNotExist(err) {
			t.Fatalf("legacy file remains: %s (%v)", deleted, err)
		}
	}
	for _, kept := range []string{unrelated, outside} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatal("unrelated file removed", err)
		}
	}
	if err := os.Mkdir(backups, 0700); err != nil {
		t.Fatal(err)
	}
	newBackup := filepath.Join(backups, "new-backup")
	if err := os.WriteFile(newBackup, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.GetNodes()) != 1 {
		t.Fatal("migration duplicated nodes")
	}
	if _, err := os.Stat(newBackup); err != nil {
		t.Fatal("restart deleted new backup", err)
	}
}

func TestCompleteBackupRestore(t *testing.T) {
	s, n, path := pingStore(t)
	now := time.Now().Unix()
	for i := 0; i < 50; i++ {
		if err := s.sdb.recordPingSample(n.UUID, "target", "192.0.2.1:80", "tcp", now-int64(i)*3600, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.json")
	if err := ExportData(path, backup); err != nil {
		t.Fatal(err)
	}
	data, err := ReadBackup(backup)
	if err != nil || len(data.Nodes[n.UUID].PingHistory["target"]) != 50 {
		t.Fatalf("incomplete backup: %v", err)
	}
	other := filepath.Join(t.TempDir(), "restored.db")
	old, err := New(other, "different")
	if err != nil {
		t.Fatal(err)
	}
	oldNode, _ := old.CreateNode("remove me", "", "")
	old.Close()
	if err := RestoreData(backup, other); err != nil {
		t.Fatal(err)
	}
	restored, err := New(other, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if !restored.VerifyAdminPassword("pass") || restored.FindNodeByToken(n.Token) == nil || restored.GetNode(oldNode.UUID) != nil {
		t.Fatal("credentials or nodes not restored")
	}
	h, err := restored.GetPingHistory(n.UUID, "target", "all")
	if err != nil || len(h.Samples) != 50 {
		t.Fatalf("history not restored: %v", err)
	}
}

func TestSQLiteTransactionRollbackAndIncrementalWrites(t *testing.T) {
	s, n, _ := pingStore(t)
	s.IngestReport(n.Token, protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 10}}}, "")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Real SQLite failure after configuration has already been written in the transaction.
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_node BEFORE UPDATE ON nodes BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNodeWithOptions(n.UUID, NodeOptions{Name: "changed", Profile: &NodeProfile{Targets: []protocol.PingTarget{}}}); err == nil {
		t.Fatal("expected SQL failure")
	}
	var count int
	s.sdb.db.QueryRow("SELECT count(*) FROM ping_history").Scan(&count)
	if count != 1 || s.GetNode(n.UUID).Name == "changed" {
		t.Fatal("failed change destroyed history or memory")
	}
	s.mu.Lock()
	prior := s.config
	s.config.SiteTitle = "uncommitted"
	s.nodes[n.UUID].Name = "changed"
	err := s.saveLocked()
	s.config = prior
	s.nodes[n.UUID].Name = n.Name
	s.mu.Unlock()
	if err == nil {
		t.Fatal("expected rollback")
	}
	cfg, _ := s.sdb.loadConfig()
	if cfg.SiteTitle != prior.SiteTitle {
		t.Fatal("config committed despite node failure")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER fail_node"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// An unchanged snapshot must not issue node or ping UPDATE/INSERT statements.
	for _, sql := range []string{
		`CREATE TRIGGER fail_node BEFORE UPDATE ON nodes BEGIN SELECT RAISE(ABORT,'redundant node write'); END`,
		`CREATE TRIGGER fail_ping BEFORE INSERT ON ping_history BEGIN SELECT RAISE(ABORT,'redundant ping write'); END`,
	} {
		if _, err := s.sdb.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryCleanupRollsBackWithDatabaseFailure(t *testing.T) {
	s, n, _ := pingStore(t)
	if _, err := s.IngestReport(n.Token, protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 10}}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_cleanup AFTER DELETE ON ping_history BEGIN SELECT RAISE(ABORT,'cleanup failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("should rollback", []protocol.PingTarget{}, ""); err == nil {
		t.Fatal("cleanup failure ignored")
	}
	cfg, err := s.sdb.loadConfig()
	if err != nil || cfg.SiteTitle == "should rollback" || len(cfg.PingTargets) != 1 {
		t.Fatalf("configuration not rolled back: %+v %v", cfg, err)
	}
	h, err := s.GetPingHistory(n.UUID, "target", "all")
	if err != nil || len(h.Samples) != 1 {
		t.Fatal("history not rolled back", err)
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER fail_cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", []protocol.PingTarget{{Name: "target", Host: "192.0.2.2:443"}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", []protocol.PingTarget{{Name: "target", Host: "192.0.2.1:80"}}, ""); err != nil {
		t.Fatal(err)
	}
	h, err = s.GetPingHistory(n.UUID, "target", "all")
	if err != nil || len(h.Samples) != 0 {
		t.Fatal("changing address back resurrected old history", err)
	}
	if _, err := s.IngestReport(n.Token, protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 10}}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(n.UUID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.sdb.db.QueryRow("SELECT count(*) FROM ping_history").Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted node retained history", err)
	}
}

func TestInvalidMigrationAndDefaultDBPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibemonitor-data.json")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := New(resolveDBPath(path), "pass"); err == nil {
		s.Close()
		t.Fatal("invalid legacy file silently ignored")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "invalid" {
		t.Fatal("invalid source overwritten")
	}
	raw, _ = json.Marshal(DataFile{Config: Config{AdminPassword: "old"}, Nodes: map[string]*Node{}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(resolveDBPath(path), "new")
	if err != nil {
		t.Fatal(err)
	}
	if !s.VerifyAdminPassword("old") {
		t.Fatal("default database path did not migrate old JSON")
	}
	s.Close()
	if err := ExportData(path, resolveDBPath(path)); err == nil {
		t.Fatal("export allowed overwriting database")
	}
}

func TestRestoreFailureLeavesDatabaseIntact(t *testing.T) {
	s, n, path := pingStore(t)
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_restore BEFORE INSERT ON config BEGIN SELECT RAISE(ABORT,'restore failed'); END`); err != nil {
		t.Fatal(err)
	}
	// Close without a new dirty update: the failure is exercised by the restore command.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.json")
	raw, _ := json.Marshal(DataFile{Config: Config{AdminPassword: "replacement"}, Nodes: map[string]*Node{}})
	if err := os.WriteFile(backup, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreData(backup, path); err == nil {
		t.Fatal("expected restore failure")
	}
	db, err := openSQLite(resolveDBPath(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nodes, err := db.loadNodes()
	if err != nil || nodes[n.UUID] == nil {
		t.Fatal("failed restore deleted original nodes", err)
	}
}
