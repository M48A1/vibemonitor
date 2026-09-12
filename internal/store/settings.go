package store

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"vibemonitor/pkg/protocol"
)

// UpdateSettings commits the complete settings change or leaves memory unchanged.
func (s *Store) UpdateSettings(title string, targets []protocol.PingTarget, password string) error {
	if err := validatePingTargets(targets); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	if title != "" || targets != nil {
		if title != "" {
			next.SiteTitle = title
		}
		if targets != nil {
			next.PingTargets = targets
		}
	}
	if password != "" {
		hashed, err := hashAdminPassword(password)
		if err != nil {
			return err
		}
		next.AdminPassword = hashed
	}
	return s.commitConfigLocked(next)
}

// UpdateSiteIcon updates the favicon URL independently of the other settings.
func (s *Store) UpdateSiteIcon(icon string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	next.SiteIcon = strings.TrimSpace(icon)
	if len(next.SiteIcon) > 2048 {
		return errors.New("site icon URL is too long")
	}
	if next.SiteIcon != "" {
		u, err := url.Parse(next.SiteIcon)
		if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || strings.HasPrefix(next.SiteIcon, "//") {
			return errors.New("site icon must be a relative URL or use http/https")
		}
	}
	return s.commitConfigLocked(next)
}

func validatePingTargets(targets []protocol.PingTarget) error {
	if len(targets) > MaxPingTargets {
		return errors.New("at most 64 ping targets are allowed")
	}
	seen := make(map[string]bool)
	for _, target := range targets {
		if seen[target.Name] {
			return errors.New("duplicate ping target name")
		}
		seen[target.Name] = true

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

	return nil
}
