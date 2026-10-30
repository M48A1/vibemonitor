package store

import (
	"fmt"
	"log"
)

// schemaVersion is the newest schema generation this build understands. It must
// match the last entry in schemaMigrations; tests compare the recorded version
// against it, so an unrecorded bump fails loudly instead of silently skipping a
// step.
//
// Databases written by builds without a version marker report version 0 and are
// brought up to date by the CREATE/ALTER statements in initSchema followed by
// the steps below.
const schemaVersion = 1

type schemaMigration struct {
	version int
	apply   func(*sqliteDB) error
}

// schemaMigrations is the source of truth for what an upgrade replays, in
// ascending order and at most once per database. A step must be idempotent:
// when it fails its version is not recorded, so the very same step runs again on
// the next start. Steps that rewrite the file (VACUUM) have to do so outside a
// transaction.
var schemaMigrations = []schemaMigration{
	{version: 1, apply: migrateDropRedundantPingIndexes},
}

// migrateSchema upgrades the file in place before the read pool exists. Upgrades
// are best effort: a read-only volume or a full disk must not keep the server
// from serving the data it already has. Nothing is recorded as done unless the
// step finished, so a skipped step is retried rather than silently lost.
func (s *sqliteDB) migrateSchema() {
	current, err := s.schemaVersion()
	if err != nil {
		log.Printf("[Store] Could not read the schema version, skipping upgrades: %v", err)
		return
	}
	for _, migration := range schemaMigrations {
		if migration.version <= current {
			continue
		}
		if err := migration.apply(s); err != nil {
			log.Printf("[Store] Schema upgrade to v%d failed (will retry): %v", migration.version, err)
			return
		}
		if err := s.setSchemaVersion(migration.version); err != nil {
			log.Printf("[Store] Could not record schema version v%d (will retry): %v", migration.version, err)
			return
		}
		log.Printf("[Store] Schema upgraded to v%d", migration.version)
	}
}

func (s *sqliteDB) schemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow("PRAGMA user_version").Scan(&version)
	return version, err
}

// PRAGMA values cannot be bound as parameters; this number comes from the
// compiled-in migration table and never from input.
func (s *sqliteDB) setSchemaVersion(version int) error {
	_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version))
	return err
}

// v1 removes the ping_history indexes older builds created. Reclaiming their
// pages is an optimisation, so it is reported but never treated as a failed
// step: retrying a whole-file rewrite on every start would be worse than
// leaving the freelist inside the file. A database that never had the indexes
// is not vacuumed here, so an upgrade only rewrites files that need it.
func migrateDropRedundantPingIndexes(s *sqliteDB) error {
	dropped, err := s.dropRedundantPingIndexes()
	if err != nil {
		return err
	}
	if !dropped {
		return nil
	}
	if err := s.reclaimFreePages(); err != nil {
		log.Printf("[Store] Index space reclaim skipped: %v", err)
	}
	return nil
}
