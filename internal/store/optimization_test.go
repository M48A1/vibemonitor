package store

import (
	"testing"

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
	if s.VerifyAdminPassword("legacy") {
		t.Fatal("plaintext password unexpectedly accepted")
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
