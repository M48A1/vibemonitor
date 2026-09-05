package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"vibemonitor/pkg/protocol"
)

// The digest prevents an unrelated sidecar being attached after a config restore
// or an interrupted two-file save. Configuration remains the authoritative file.
type pingFile struct {
	DataDigest string              `json:"data_digest"`
	Nodes      map[string]pingNode `json:"nodes"`
}
type pingNode struct {
	History map[string][]PingSample `json:"history,omitempty"`
	Results []protocol.PingResult   `json:"results,omitempty"`
}

func dataDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s *Store) nodesWithoutPing() map[string]*Node {
	nodes := make(map[string]*Node, len(s.nodes))
	for id, node := range s.nodes {
		copy := *node
		copy.PingHistory = nil
		if node.LastReport != nil {
			report := *node.LastReport
			report.PingResults = nil
			copy.LastReport = &report
		}
		nodes[id] = &copy
	}
	return nodes
}
func (s *Store) savePingFile(data []byte) error {
	pf := pingFile{DataDigest: dataDigest(data), Nodes: make(map[string]pingNode)}
	for id, node := range s.nodes {
		entry := pingNode{History: node.PingHistory}
		if node.LastReport != nil {
			entry.Results = node.LastReport.PingResults
		}
		pf.Nodes[id] = entry
	}
	encoded, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	path := s.filePath + ".ping.json"
	if err := os.WriteFile(path+".tmp", encoded, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (s *Store) loadPingFile(data []byte) error {
	path := s.filePath + ".ping.json"
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // Also migrates inline history from older releases.
	if err != nil {
		return err
	}
	var pf pingFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return fmt.Errorf("invalid ping data %s: %w", path, err)
	}
	if pf.DataDigest != dataDigest(data) {
		return nil
	}
	for id, entry := range pf.Nodes {
		if node := s.nodes[id]; node != nil {
			node.PingHistory = entry.History
			if node.LastReport != nil {
				node.LastReport.PingResults = entry.Results
			}
		}
	}
	return nil
}
