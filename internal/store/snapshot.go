package store

import (
	"encoding/json"
	"fmt"
)

// saveSnapshot commits configuration, changed nodes and history together.
// Caches are advanced only after commit, so failed writes can be retried.
func (s *sqliteDB) saveSnapshot(config Config, nodes map[string]*Node, replace bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	w := &sqliteDB{db: s.db, tx: tx, nodeCache: s.nodeCache}
	if replace {
		w.nodeCache = nil
		if _, err = tx.Exec("DELETE FROM ping_history"); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM nodes"); err != nil {
			return err
		}
	}
	if err = w.saveConfig(&config); err != nil {
		return err
	}
	if err = w.saveAllNodes(nodes); err != nil {
		return err
	}
	if s.nodeCache == nil {
		if _, err = tx.Exec("DELETE FROM ping_history WHERE node_uuid NOT IN (SELECT uuid FROM nodes)"); err != nil {
			return err
		}
	} else {
		for id := range s.nodeCache {
			if _, exists := nodes[id]; !exists {
				if _, err = tx.Exec("DELETE FROM ping_history WHERE node_uuid=?", id); err != nil {
					return err
				}
			}
		}
	}
	nextNodes := make(map[string]string, len(nodes))
	nextTargets := make(map[string]string, len(nodes))
	nextPings := make(map[string]PingSample)
	stmt, err := tx.Prepare(`INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency)
		VALUES(?,?,?,?,?,?) ON CONFLICT(node_uuid,target_name,timestamp) DO UPDATE SET
		host=excluded.host,method=excluded.method,latency=excluded.latency`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, n := range nodes {
		clone := *n
		clone.PingHistory = nil
		raw, err := json.Marshal(clone)
		if err != nil {
			return err
		}
		nextNodes[id] = string(raw)
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
		if replace || s.targetCache[id] != string(targetJSON) {
			if _, err = tx.Exec(`DELETE FROM ping_history WHERE node_uuid=? AND NOT EXISTS
				(SELECT 1 FROM json_each(?) WHERE json_extract(value,'$.name')=target_name AND json_extract(value,'$.host')=host)`, id, string(targetJSON)); err != nil {
				return err
			}
		}
		for name, samples := range n.PingHistory {
			for _, sample := range samples {
				if host, ok := allowed[name]; !ok || host != sample.Host || (sample.Method != "tcp" && sample.Method != "icmp" && sample.Method != "unknown") {
					continue
				}
				key := fmt.Sprintf("%s/%s/%d", id, name, sample.Timestamp)
				nextPings[key] = sample
				if prior, ok := s.pingCache[key]; !replace && ok && prior == sample {
					continue
				}
				if _, err = stmt.Exec(id, name, sample.Host, sample.Method, sample.Timestamp, sample.Latency); err != nil {
					return err
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.nodeCache, s.pingCache = nextNodes, nextPings
	s.targetCache = nextTargets
	return nil
}
