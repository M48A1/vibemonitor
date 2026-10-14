package store

import (
	"errors"
	"testing"
)

func TestReorderNodesPersistenceAndInvalidOrder(t *testing.T) {
	s := deletionStore(t)
	if err := s.ReorderNodes(map[string]int{"survivor": 1, "deleted": 2, "unknown": 3}); err != nil {
		t.Fatal(err)
	}
	if s.GetNode("survivor").Weight != 1 || s.GetNode("deleted").Weight != 2 {
		t.Fatal("display order not updated")
	}
	var weight int
	if err := s.sdb.db.QueryRow("SELECT json_extract(data_json, '$.weight') FROM nodes WHERE uuid = ?", "survivor").Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight != 1 {
		t.Fatal("display order not persisted")
	}
	if err := s.ReorderNodes(map[string]int{"survivor": 4, "deleted": -1}); !errors.Is(err, ErrInvalidNodeOrder) {
		t.Fatalf("invalid order accepted: %v", err)
	}
	if s.GetNode("survivor").Weight != 1 || s.GetNode("deleted").Weight != 2 {
		t.Fatal("invalid order changed existing weights")
	}
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_reorder BEFORE UPDATE ON config BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderNodes(map[string]int{"survivor": 4}); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if s.GetNode("survivor").Weight != 1 {
		t.Fatal("failed persistence changed display order")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER fail_reorder"); err != nil {
		t.Fatal(err)
	}
}
