package store

import (
	"context"
	"time"
)

// Self-registration is time-boxed on purpose. The discovery key is a bearer
// credential that creates a node and hands back its token, so a key that never
// expires is a standing invitation: leaked once, it stays useful forever. A
// window is opened from the panel for one install, and closes on its own.
const (
	// DefaultRegistrationMinutes is the window the panel offers first.
	DefaultRegistrationMinutes = 60
	// MaxRegistrationMinutes bounds a window at one day. Anything longer is a
	// standing key with extra steps.
	MaxRegistrationMinutes = 24 * 60
)

// OpenRegistration rotates the discovery key and makes it valid until now plus
// minutes. Rotating on every open means a command from an earlier window cannot
// be replayed inside a later one.
func (s *Store) OpenRegistration(minutes int) (string, time.Time, error) {
	return s.OpenRegistrationContext(context.Background(), minutes)
}

func (s *Store) OpenRegistrationContext(ctx context.Context, minutes int) (string, time.Time, error) {
	if minutes <= 0 {
		minutes = DefaultRegistrationMinutes
	}
	if minutes > MaxRegistrationMinutes {
		minutes = MaxRegistrationMinutes
	}
	key := GenerateToken(16)
	until := time.Now().Add(time.Duration(minutes) * time.Minute).UTC()

	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	next.AutoDiscoveryKey = key
	next.RegisterUntil = until.Unix()
	if err := s.commitConfigLockedContext(ctx, next); err != nil {
		return "", time.Time{}, err
	}
	return key, until, nil
}

// CloseRegistration ends the window early and forgets the key.
func (s *Store) CloseRegistration() error {
	return s.CloseRegistrationContext(context.Background())
}

func (s *Store) CloseRegistrationContext(ctx context.Context) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	next.AutoDiscoveryKey = ""
	next.RegisterUntil = 0
	return s.commitConfigLockedContext(ctx, next)
}

// RegistrationOpen reports the key an open window accepts, and whether one is
// open at all. Both are read under one lock so a window closing between the two
// reads cannot let a stale key through.
func (s *Store) RegistrationOpen(now time.Time) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.AutoDiscoveryKey == "" || s.config.RegisterUntil <= now.Unix() {
		return "", false
	}
	return s.config.AutoDiscoveryKey, true
}
