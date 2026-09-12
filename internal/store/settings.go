package store

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"vibemonitor/pkg/protocol"
)

var ErrInvalidSettings = errors.New("invalid settings")

// UpdateSettings commits the complete settings change or leaves memory unchanged.
func (s *Store) UpdateSettings(title string, targets []protocol.PingTarget, password string) error {
	return s.UpdateSettingsWithIcon(title, targets, password, nil)
}

// UpdateSettingsWithIcon validates and commits all submitted settings atomically.
func (s *Store) UpdateSettingsWithIcon(title string, targets []protocol.PingTarget, password string, icon *string) error {
	if err := validatePingTargets(targets); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	if icon != nil {
		if err := validateSiteIcon(*icon); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSettings, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	if icon != nil {
		next.SiteIcon = strings.TrimSpace(*icon)
	}
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
	if err := validateSiteIcon(icon); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	next.SiteIcon = strings.TrimSpace(icon)
	return s.commitConfigLocked(next)
}

func validateSiteIcon(icon string) error {
	icon = strings.TrimSpace(icon)
	if len(icon) > 2048 {
		return errors.New("site icon URL is too long")
	}
	if icon != "" {
		u, err := url.Parse(icon)
		if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || strings.HasPrefix(icon, "//") {
			return errors.New("site icon must be a relative URL or use http/https")
		}
	}
	return nil
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
