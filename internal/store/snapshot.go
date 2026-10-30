package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// saveSnapshotContext commits configuration, changed nodes and history together.
// Caches are advanced only after commit, so failed writes can be retried, and
// the transaction obeys the caller's context: a cancelled flush rolls back and
// leaves its samples pending.
func (s *sqliteDB) saveSnapshotContext(ctx context.Context, config Config, nodes map[string]*Node, samples ...historySample) error {
	return s.saveSnapshotRowsContext(ctx, config, nodes, true, samples...)
}

// A metadata edit must not copy, serialize or persist unrelated nodes/history.
// The caller holds persistMu and supplies an owned copy of the edited node.
func (s *sqliteDB) saveNodeSnapshotContext(ctx context.Context, config Config, node *Node) error {
	return s.saveSnapshotRowsContext(ctx, config, map[string]*Node{node.UUID: node}, false)
}

// saveDirtyNodesContext persists only the supplied nodes plus any pending
// history samples in one transaction. Config is intentionally not written here:
// every config mutation is already committed immediately by its own path.
func (s *sqliteDB) saveDirtyNodesContext(ctx context.Context, config Config, nodes map[string]*Node, samples ...historySample) error {
	return s.saveSnapshotRowsContext(ctx, config, nodes, false, samples...)
}

func (s *sqliteDB) saveSnapshotRowsContext(ctx context.Context, config Config, nodes map[string]*Node, replaceAll bool, samples ...historySample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := &sqliteDB{db: s.db, tx: tx, nodeCache: s.nodeCache}
	var nextNodes map[string]string
	if replaceAll {
		if err = w.saveConfigContext(ctx, &config); err != nil {
			return err
		}
		nextNodes, err = w.saveAllNodesContext(ctx, nodes)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM telegram_alert_state WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
			return err
		}
		if s.nodeCache == nil {
			if _, err = tx.ExecContext(ctx, "DELETE FROM ping_history WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM resource_history WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
				return err
			}
			for _, table := range []string{"history_rollups", "history_rollup_dirty"} {
				if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
					return err
				}
			}
		} else {
			for id := range s.nodeCache {
				if _, exists := nodes[id]; !exists {
					if _, err = tx.ExecContext(ctx, "DELETE FROM ping_history WHERE node_uuid=?", id); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, "DELETE FROM resource_history WHERE node_uuid=?", id); err != nil {
						return err
					}
					for _, table := range []string{"history_rollups", "history_rollup_dirty"} {
						if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE node_uuid=?", id); err != nil {
							return err
						}
					}
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM ping_observations WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
			return err
		}
	} else {
		nextNodes, err = w.saveNodeRowsContext(ctx, nodes)
		if err != nil {
			return err
		}
	}
	observations, err := tx.PrepareContext(ctx, pingObservationUpsert)
	if err != nil {
		return err
	}
	defer observations.Close()
	nextTargets := make(map[string]string, len(nodes))
	nextPings := make(map[string]PingSample)
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency)
		VALUES(?,?,?,?,?,?) ON CONFLICT(node_uuid,target_name,timestamp) DO UPDATE SET
		host=excluded.host,method=excluded.method,latency=excluded.latency`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, n := range nodes {
		targets := config.PingTargets
		if n.Profile != nil {
			targets = n.Profile.Targets
		}
		allowed := make(map[string]string, len(targets))
		for _, target := range targets {
			allowed[target.Name] = target.Host
		}
		targetJSON, _ := json.Marshal(targets)
		nextTargets[id] = string(targetJSON)
		if s.targetCache[id] != string(targetJSON) {
			if _, err = tx.ExecContext(ctx, `DELETE FROM ping_history WHERE node_uuid=? AND NOT EXISTS
				(SELECT 1 FROM json_each(?) WHERE json_extract(value,'$.name')=target_name AND json_extract(value,'$.host')=host)`, id, string(targetJSON)); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM history_rollups WHERE node_uuid=? AND kind='ping' AND NOT EXISTS
				(SELECT 1 FROM json_each(?) WHERE json_extract(value,'$.name')=target_name AND json_extract(value,'$.host')=host)`, id, string(targetJSON)); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM ping_observations WHERE node_uuid=? AND NOT EXISTS
			(SELECT 1 FROM json_each(?) WHERE json_extract(value,'$.name')=target_name AND json_extract(value,'$.host')=host)`, id, string(targetJSON)); err != nil {
				return err
			}
		}
		for name, observation := range n.PingObserved {
			if host, ok := allowed[name]; ok && host == observation.Host {
				if _, err = observations.ExecContext(ctx, id, name, observation.Host, observation.Method, observation.StartedAt, observation.LastSampleAt); err != nil {
					return err
				}
			}
		}
		for name, samples := range n.PingHistory {
			for _, sample := range samples {
				if host, ok := allowed[name]; !ok || host != sample.Host || (sample.Method != "tcp" && sample.Method != "icmp" && sample.Method != "unknown") {
					continue
				}
				key := fmt.Sprintf("%s/%s/%d", id, name, sample.Timestamp)
				nextPings[key] = sample
				if prior, ok := s.pingCache[key]; ok && prior == sample {
					continue
				}
				if _, err = stmt.ExecContext(ctx, id, name, sample.Host, sample.Method, sample.Timestamp, sample.Latency); err != nil {
					return err
				}
			}
		}
	}
	if err = writeHistorySamplesContext(ctx, tx, filterHistorySamples(nodes, config, samples)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if replaceAll {
		s.nodeCache, s.pingCache = nextNodes, nextPings
		s.targetCache = nextTargets
	} else {
		if s.nodeCache == nil {
			s.nodeCache = make(map[string]string)
		}
		if s.targetCache == nil {
			s.targetCache = make(map[string]string)
		}
		if s.pingCache == nil {
			s.pingCache = make(map[string]PingSample)
		}
		for id, value := range nextNodes {
			s.nodeCache[id] = value
		}
		for id, value := range nextTargets {
			// Only target changes can invalidate already persisted samples.
			if s.targetCache[id] != value {
				for key := range s.pingCache {
					if strings.HasPrefix(key, id+"/") {
						delete(s.pingCache, key)
					}
				}
			}
			s.targetCache[id] = value
		}
		for key, sample := range nextPings {
			s.pingCache[key] = sample
		}
	}
	return nil
}
