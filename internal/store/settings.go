package store

import (
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"

	"vibemonitor/pkg/protocol"
)

// UpdateSettings commits the complete settings change or leaves memory unchanged.
func (s *Store) UpdateSettings(title, announcement string, targets []protocol.PingTarget, password string) error {
	if len(targets) > 64 {
		return errors.New("at most 64 ping targets are allowed")
	}
	for _, target := range targets {
		if target.Name == "" || len(target.Name) > 128 || len(target.Host) > 253 {
			return errors.New("invalid ping target")
		}
		host := target.Host
		if strings.Contains(host, ":") {
			var port string
			var err error
			host, port, err = net.SplitHostPort(host)
			n, _ := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return errors.New("invalid target port")
			}
		}
		if host == "" || strings.HasPrefix(host, "-") {
			return errors.New("invalid target host")
		}
		for _, c := range host {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
				return errors.New("use an IPv4 address or domain name")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.config
	if title != "" || announcement != "" || targets != nil {
		if title != "" {
			s.config.SiteTitle = title
		}
		s.config.Announcement = announcement
		if targets != nil {
			s.config.PingTargets = targets
		}
	}
	if password != "" {
		s.config.AdminPassword = password
	}
	if err := s.saveLocked(); err != nil {
		s.config = previous
		return err
	}
	s.notifyUpdate()
	return nil
}

// ValidateData checks a backup without modifying or starting a store.
func ValidateData(data []byte) error {
	var df DataFile
	if err := json.Unmarshal(data, &df); err != nil {
		return err
	}
	if df.Config.AdminPassword == "" || df.Nodes == nil {
		return errors.New("backup is missing configuration or nodes")
	}
	tokens := make(map[string]bool)
	for id, node := range df.Nodes {
		if node == nil || id == "" || node.UUID != id || node.Token == "" || tokens[node.Token] {
			return errors.New("backup contains invalid or duplicate nodes")
		}
		tokens[node.Token] = true
	}
	return nil
}
