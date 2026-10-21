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

func TestNodeCreationAndEditingPreserveDisplayOrder(t *testing.T) {
	s := deletionStore(t)
	if err := s.ReorderNodes(map[string]int{"survivor": 1, "deleted": 2}); err != nil {
		t.Fatal(err)
	}
	// A legacy creation request must not insert ahead of the saved order.
	node, err := s.CreateNodeWithOptions(NodeOptions{Name: "AAA", Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	if node.Weight != 3 {
		t.Fatalf("new node weight = %d, want 3", node.Weight)
	}
	if err := s.ReorderNodes(map[string]int{node.UUID: 1, "survivor": 2, "deleted": 3}); err != nil {
		t.Fatal(err)
	}
	// Editing with a stale weight must not undo the latest sort operation.
	if err := s.UpdateNode(node.UUID, "ZZZ", "", "", 3); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(node.UUID); got.Weight != 1 || got.Name != "ZZZ" {
		t.Fatalf("edit changed display order or failed to rename: %+v", got)
	}
	var weight int
	if err := s.sdb.db.QueryRow("SELECT json_extract(data_json, '$.weight') FROM nodes WHERE uuid = ?", node.UUID).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight != 1 {
		t.Fatalf("persisted weight = %d, want 1", weight)
	}
	next, err := s.CreateNode("Another", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if next.Weight != 4 {
		t.Fatalf("next node weight = %d, want 4", next.Weight)
	}
}
