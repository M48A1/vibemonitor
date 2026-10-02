package store

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
)

func TestTrafficWarningValidation(t *testing.T) {
	for _, percent := range []float64{-1, 100.1, math.NaN(), math.Inf(1)} {
		if !errors.Is(validateProfile(&NodeProfile{TrafficWarningPercent: percent}), ErrInvalidTrafficWarning) {
			t.Fatalf("accepted invalid warning percentage %v", percent)
		}
	}
	for _, percent := range []float64{0, 0.5, 80, 100} {
		if err := validateProfile(&NodeProfile{TrafficWarningPercent: percent}); err != nil {
			t.Fatalf("rejected valid warning percentage %v: %v", percent, err)
		}
	}
}

func TestTrafficWarningPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateNodeWithOptions(NodeOptions{Name: "Tokyo", TrafficLimitGB: 1000,
		Profile: &NodeProfile{TrafficWarningPercent: 80}})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	state := TelegramAlertState{TrafficWarningCycle: "2026-10-01T00:00:00Z"}
	if err := s.SaveTelegramAlertState("test", node.UUID, state); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.GetNode(node.UUID).Profile.TrafficWarningPercent; got != 80 {
		t.Fatalf("warning percentage after restart = %v", got)
	}
	states, err := s.LoadTelegramAlertStates("test")
	if err != nil || states[node.UUID].TrafficWarningCycle != state.TrafficWarningCycle {
		t.Fatalf("warning state not persisted: %v, %v", states, err)
	}
	if err := s.UpdateNodeWithOptions(node.UUID, NodeOptions{Profile: &NodeProfile{},
		ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1}); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(node.UUID).Profile.TrafficWarningPercent; got != 0 {
		t.Fatalf("could not disable warning: %v", got)
	}
}
