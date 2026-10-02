package store

import (
	"database/sql"
	"fmt"
)

// Bound retry memory if the disk remains unavailable. A rejected sample does not
// advance the sampling clock, so the next report retries after space is freed.
const maxPendingHistorySamples = 65536

type historySample struct {
	UUID, Target, Host, Method string
	ObservationStart           int64
	Timestamp                  int64
	CPU, RAM                   sql.NullFloat64
	Network                    sql.NullInt64
	Latency                    int
	Ping                       bool
}

func (s *Store) enqueueHistoryLocked(sample historySample) bool {
	if len(s.pendingSamples) >= maxPendingHistorySamples {
		return false
	}
	s.pendingSamples = append(s.pendingSamples, sample)
	return true
}

func writeHistorySamples(tx *sql.Tx, samples []historySample) error {
	if len(samples) == 0 {
		return nil
	}
	resource, err := tx.Prepare(`INSERT INTO resource_history(node_uuid,timestamp,cpu_usage,ram_usage,network_rate) VALUES(?,?,?,?,?)
 ON CONFLICT(node_uuid,timestamp) DO UPDATE SET cpu_usage=excluded.cpu_usage,ram_usage=excluded.ram_usage,network_rate=excluded.network_rate`)
	if err != nil {
		return err
	}
	defer resource.Close()
	ping, err := tx.Prepare(`INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency) VALUES(?,?,?,?,?,?)
 ON CONFLICT(node_uuid,target_name,timestamp) DO UPDATE SET host=excluded.host,method=excluded.method,latency=excluded.latency`)
	if err != nil {
		return err
	}
	defer ping.Close()
	observation, err := tx.Prepare(pingObservationUpsert)
	if err != nil {
		return err
	}
	defer observation.Close()
	for _, v := range samples {
		if v.Ping {
			_, err = ping.Exec(v.UUID, v.Target, v.Host, v.Method, v.Timestamp, v.Latency)
			if err == nil && v.ObservationStart > 0 {
				_, err = observation.Exec(v.UUID, v.Target, v.Host, v.Method, v.ObservationStart, v.Timestamp)
			}
		} else {
			_, err = resource.Exec(v.UUID, v.Timestamp, v.CPU, v.RAM, v.Network)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) flushHistory() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.RLock()
	count := len(s.pendingSamples)
	batch := filterHistorySamples(s.nodes, s.config, s.pendingSamples)
	s.mu.RUnlock()
	if count == 0 {
		return nil
	}
	tx, err := s.sdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeHistorySamples(tx, batch); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.sdb.pingCache == nil {
		s.sdb.pingCache = make(map[string]PingSample)
	}
	for _, v := range batch {
		if v.Ping {
			s.sdb.pingCache[fmt.Sprintf("%s/%s/%d", v.UUID, v.Target, v.Timestamp)] = PingSample{Host: v.Host, Method: v.Method, Timestamp: v.Timestamp, Latency: v.Latency}
		}
	}
	s.mu.Lock()
	s.ackHistoryLocked(count)
	s.mu.Unlock()
	return nil
}

// Requires mu. Preserve samples appended by concurrent ingestion after capture.
func (s *Store) ackHistoryLocked(count int) {
	clear(s.pendingSamples[:count])
	s.pendingSamples = s.pendingSamples[count:]
	if len(s.pendingSamples) == 0 {
		s.pendingSamples = nil
	}
}

// These copies own every collection modified in place by report ingestion.
// Reports, basic info and profiles are replaced as whole values by the store.
func cloneNodeForSave(n *Node) *Node {
	c := *n
	c.History = append([]HistoryPoint(nil), n.History...)
	c.PingObserved = make(map[string]PingObservation, len(n.PingObserved))
	for k, v := range n.PingObserved {
		c.PingObserved[k] = v
	}
	c.PingHistory = make(map[string][]PingSample, len(n.PingHistory))
	for k, v := range n.PingHistory {
		c.PingHistory[k] = append([]PingSample(nil), v...)
	}
	return &c
}
func cloneNodesForSave(nodes map[string]*Node) map[string]*Node {
	copies := make(map[string]*Node, len(nodes))
	for id, n := range nodes {
		copies[id] = cloneNodeForSave(n)
	}
	return copies
}

// Filter against the same configuration snapshot as the node write. A pending
// retry must never resurrect a deleted node or a removed/renamed ping target.
func filterHistorySamples(nodes map[string]*Node, config Config, samples []historySample) []historySample {
	kept := make([]historySample, 0, len(samples))
	for _, sample := range samples {
		n := nodes[sample.UUID]
		if n == nil {
			continue
		}
		if sample.Ping {
			targets := config.PingTargets
			if n.Profile != nil {
				targets = n.Profile.Targets
			}
			allowed := false
			for _, target := range targets {
				if target.Name == sample.Target && target.Host == sample.Host {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		kept = append(kept, sample)
	}
	return kept
}
