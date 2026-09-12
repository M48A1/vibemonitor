package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func legacyCleanupFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "data.json")
	raw, err := json.Marshal(DataFile{Config: Config{AdminPassword: "pass", SiteTitle: "original"}, Nodes: map[string]*Node{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "backups")
	if err := os.Mkdir(backups, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "old"), []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	return source, backups
}

func TestMigrationFailureRetainsAllSources(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER fail_import BEFORE INSERT ON config BEGIN SELECT RAISE(ABORT,'write failure'); END`,
		`CREATE TRIGGER corrupt_import AFTER INSERT ON config BEGIN UPDATE config SET site_title='wrong' WHERE id=1; END`,
	} {
		t.Run(trigger, func(t *testing.T) {
			source, backups := legacyCleanupFixture(t)
			db, err := openSQLite(resolveDBPath(source))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := db.migrateLegacy(source); err == nil {
				t.Fatal("failed import or verification was accepted")
			}
			for _, path := range []string{source, filepath.Join(backups, "old")} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("failure removed recovery material", err)
				}
			}
		})
	}
}

func TestMigrationRejectsSymlinkBackupDirectory(t *testing.T) {
	source, backups := legacyCleanupFixture(t)
	other := filepath.Join(filepath.Dir(source), "external-backups")
	if err := os.Rename(backups, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, backups); err != nil {
		t.Fatal(err)
	}
	if s, err := New(source, ""); err == nil {
		s.Close()
		t.Fatal("symlink cleanup target accepted")
	}
	for _, path := range []string{source, filepath.Join(other, "old")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("symlink preflight deleted data", err)
		}
	}
}

func TestMigrationCleanupRefusesChangedTargets(t *testing.T) {
	source, backups := legacyCleanupFixture(t)
	cleanup, err := migrationCleanupTargets(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed during import"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.remove(); err == nil {
		t.Fatal("changed source deleted")
	}
	if _, err := os.Stat(filepath.Join(backups, "old")); err != nil {
		t.Fatal("backup deleted before preflight completed", err)
	}
}

func TestMigrationRejectsDatabaseSymlinkIntoBackups(t *testing.T) {
	source, backups := legacyCleanupFixture(t)
	if err := os.Symlink(filepath.Join(backups, "old"), resolveDBPath(source)); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationCleanupTargets(source); err == nil {
		t.Fatal("database symlink into cleanup directory accepted")
	}
	if _, err := os.Stat(filepath.Join(backups, "old")); err != nil {
		t.Fatal(err)
	}
}
