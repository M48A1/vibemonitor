package store

import "vibemonitor/pkg/protocol"

// Names are unique display identifiers. The host and measurement method are
// retained in each sample so a name cannot silently relabel older observations.
func filterPingResults(results []protocol.PingResult, targets []protocol.PingTarget) []protocol.PingResult {
	allowed := make(map[string]string, len(targets))
	for _, target := range targets {
		allowed[target.Name] = target.Host
	}
	seen := make(map[string]bool)
	filtered := make([]protocol.PingResult, 0, min(len(results), MaxPingTargets))
	for _, result := range results {
		host, ok := allowed[result.Name]
		if !ok || host != result.Host || seen[result.Name] {
			continue
		}
		switch result.Method {
		case "":
			result.Method = "unknown" // Compatibility with older probes, never assumed to be ICMP.
		case "tcp", "icmp", "unknown":
		default:
			continue
		}
		seen[result.Name] = true
		filtered = append(filtered, result)
	}
	return filtered
}

func (s *Store) prunePingDataLocked() {
	allowed := make(map[string]string, len(s.config.PingTargets))
	for _, target := range s.config.PingTargets {
		allowed[target.Name] = target.Host
	}
	for _, node := range s.nodes {
		clean := make(map[string][]PingSample)
		for name, host := range allowed {
			var samples []PingSample
			for _, sample := range node.PingHistory[name] {
				// Legacy samples have no trustworthy host/method provenance.
				if sample.Host == host && (sample.Method == "tcp" || sample.Method == "icmp" || sample.Method == "unknown") {
					samples = append(samples, sample)
				}
			}
			if len(samples) > MaxPingSamplesPerTarget {
				samples = samples[len(samples)-MaxPingSamplesPerTarget:]
			}
			if len(samples) > 0 {
				clean[name] = samples
			}
		}
		node.PingHistory = clean
		if node.LastReport != nil {
			report := *node.LastReport
			report.PingResults = filterPingResults(report.PingResults, s.config.PingTargets)
			node.LastReport = &report
		}
	}
}

// Config and the corresponding removal of stale history are one transaction.
func (s *Store) commitConfigLocked(next Config) error {
	previous := s.config
	type pingState struct {
		history map[string][]PingSample
		report  *protocol.Report
	}
	states := make(map[string]pingState, len(s.nodes))
	for id, node := range s.nodes {
		states[id] = pingState{node.PingHistory, node.LastReport}
	}
	s.config = next
	s.prunePingDataLocked()
	if err := s.saveLocked(); err != nil {
		s.config = previous
		for id, state := range states {
			s.nodes[id].PingHistory = state.history
			s.nodes[id].LastReport = state.report
		}
		return err
	}
	s.notifyUpdate()
	return nil
}
