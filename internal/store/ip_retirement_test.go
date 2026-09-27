package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyNodeIPsDroppedOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	st, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.CreateNode("test", "", "JP")
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow("SELECT data_json FROM nodes WHERE uuid = ?", node.UUID).Scan(&stored); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal([]byte(stored), &legacy); err != nil {
		db.Close()
		t.Fatal(err)
	}
	legacy["client_ip"] = "198.51.100.7"
	legacy["basic_info"] = map[string]any{"os": "Linux", "ipv4": "203.0.113.9", "ipv6": "2001:db8::9"}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE nodes SET data_json = ? WHERE uuid = ?", string(encoded), node.UUID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.GetNode(node.UUID).Region; got != "JP" {
		st.Close()
		t.Fatalf("region changed during migration: %q", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow("SELECT data_json FROM nodes WHERE uuid = ?", node.UUID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"client_ip", "ipv4", "ipv6", "198.51.100.7", "203.0.113.9", "2001:db8::9"} {
		if strings.Contains(stored, value) {
			t.Fatalf("legacy IP data remained after save: %q", value)
		}
	}
}
