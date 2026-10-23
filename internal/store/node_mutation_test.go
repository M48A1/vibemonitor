package store

import (
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestEditPersistsOnlySelectedNode(t *testing.T) {
	s := deletionStore(t)
	// An unrelated report has arrived since the last save. Editing another node
	// must neither write it nor discard its pending persistence/cache state.
	if _, err := s.IngestBasicInfo("deleted-token", protocol.BasicInfo{OS: "pending"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TRIGGER reject_config BEFORE UPDATE ON config BEGIN SELECT RAISE(ABORT,'unrelated config write'); END`,
		`CREATE TRIGGER reject_other_node BEFORE UPDATE ON nodes WHEN NEW.uuid='deleted' BEGIN SELECT RAISE(ABORT,'unrelated node write'); END`,
		`CREATE TRIGGER reject_other_history BEFORE DELETE ON ping_history WHEN OLD.node_uuid='deleted' BEGIN SELECT RAISE(ABORT,'unrelated history cleanup'); END`,
	} {
		if _, err := s.sdb.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	otherCache := s.sdb.nodeCache["deleted"]
	if err := s.UpdateNode("survivor", "renamed", "", "", 0); err != nil {
		t.Fatal(err)
	}
	disk, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(disk) != 2 || disk["survivor"].Name != "renamed" || disk["deleted"].BasicInfo != nil {
		t.Fatal("edit wrote or removed unrelated data")
	}
	if s.sdb.nodeCache["deleted"] != otherCache {
		t.Fatal("edit advanced unrelated cache")
	}
	for _, trigger := range []string{"reject_config", "reject_other_node", "reject_other_history"} {
		if _, err := s.sdb.db.Exec("DROP TRIGGER " + trigger); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	disk, err = s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if disk["deleted"].BasicInfo == nil || disk["deleted"].BasicInfo.OS != "pending" {
		t.Fatal("later save lost unrelated report")
	}
}

func TestEditTargetFailureRollsBackNodeAndCaches(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "before", Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "old.example.com:443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	report := perfReport(time.Now())
	report.PingResults = []protocol.PingResult{{Name: "route", Host: "old.example.com:443", Method: "tcp", Latency: 10}}
	if _, err := s.IngestReport(n.Token, report); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	cachedNode, cachedTargets := s.sdb.nodeCache[n.UUID], s.sdb.targetCache[n.UUID]
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER reject_cleanup BEFORE DELETE ON ping_history BEGIN SELECT RAISE(ABORT,'cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	opts := NodeOptions{Name: "after", ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1, Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "new.example.com:443"}}}}
	if err := s.UpdateNodeWithOptions(n.UUID, opts); err == nil {
		t.Fatal("expected failure")
	}
	disk, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if disk[n.UUID].Name != "before" || s.GetNode(n.UUID).Name != "before" || s.sdb.nodeCache[n.UUID] != cachedNode || s.sdb.targetCache[n.UUID] != cachedTargets {
		t.Fatal("failed edit published state")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER reject_cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNodeWithOptions(n.UUID, opts); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	assertDBRowCount(t, s.sdb, "ping_history", 0)
	if s.GetNode(n.UUID).Profile.Targets[0].Host != "new.example.com:443" {
		t.Fatal("retry did not apply target")
	}
}

func TestDeleteNodeSkipsRedundantRollupInvalidation(t *testing.T) {
	s := deletionStore(t)
	for _, query := range []string{
		`UPDATE history_rollup_state SET completed_before=3600`,
		`CREATE TRIGGER reject_dirty BEFORE INSERT ON history_rollup_dirty BEGIN SELECT RAISE(ABORT,'redundant invalidation'); END`,
	} {
		if _, err := s.sdb.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteNode("deleted"); err != nil {
		t.Fatal(err)
	}
	assertDBRowCount(t, s.sdb, "history_rollup_delete_guard", 0)
	// Suppression must not leak into ordinary raw-history deletion.
	if _, err := s.sdb.db.Exec(`DELETE FROM resource_history WHERE node_uuid='survivor'`); err == nil {
		t.Fatal("ordinary deletion bypassed invalidation")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER reject_dirty"); err != nil {
		t.Fatal(err)
	}
}
