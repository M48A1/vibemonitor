package store

import (
	"errors"
	"math"
	"net"
	"time"
	"vibemonitor/pkg/protocol"
)

// A nil profile preserves the global targets of legacy nodes.
type NodeProfile struct {
	TrafficWarningPercent float64               `json:"traffic_warning_percent,omitempty"`
	Targets               []protocol.PingTarget `json:"targets"`
	DueDate               string                `json:"due_date"`
	PaymentCycle          string                `json:"payment_cycle"`
	Price                 float64               `json:"price"`
	Currency              string                `json:"currency"`
	CPUThreshold          *float64              `json:"cpu_threshold"`
	AlertsDisabled        bool                  `json:"alerts_disabled,omitempty"`
}

var ErrInvalidTrafficWarning = errors.New("traffic warning percentage must be between 0 and 100; 0 disables early warnings")

type PingPreview struct {
	Name    string       `json:"name"`
	Host    string       `json:"host"`
	Method  string       `json:"method"`
	Loss    *float64     `json:"loss"`
	Samples []PingSample `json:"samples"`
}

func validateProfile(p *NodeProfile) error {
	if p == nil {
		return nil
	}
	if math.IsNaN(p.TrafficWarningPercent) || math.IsInf(p.TrafficWarningPercent, 0) || p.TrafficWarningPercent < 0 || p.TrafficWarningPercent > 100 {
		return ErrInvalidTrafficWarning
	}
	if err := validatePingTargets(p.Targets); err != nil {
		return err
	}
	for _, target := range p.Targets {
		if _, _, err := net.SplitHostPort(target.Host); err != nil {
			return errors.New("TCP target requires host:port")
		}
	}
	if p.DueDate != "" {
		if _, err := time.Parse("2006-01-02", p.DueDate); err != nil {
			return errors.New("invalid due date")
		}
	}
	switch p.PaymentCycle {
	case "", "month", "quarter", "year":
	default:
		return errors.New("invalid payment cycle")
	}
	if math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price < 0 || p.Price > 1e9 {
		return errors.New("invalid price")
	}
	if p.CPUThreshold != nil && (math.IsNaN(*p.CPUThreshold) || math.IsInf(*p.CPUThreshold, 0) || *p.CPUThreshold < 0 || *p.CPUThreshold > 100) {
		return errors.New("invalid CPU threshold")
	}
	switch p.Currency {
	case "", "CNY", "USD", "EUR", "HKD", "JPY", "GBP":
	default:
		return errors.New("invalid currency")
	}
	return nil
}
func (s *Store) targetsLocked(n *Node) []protocol.PingTarget {
	if n.Profile != nil {
		return n.Profile.Targets
	}
	return s.config.PingTargets
}
func (s *Store) NodeTargets(uuid string) []protocol.PingTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n := s.nodes[uuid]; n != nil {
		return append([]protocol.PingTarget{}, s.targetsLocked(n)...)
	}
	return []protocol.PingTarget{}
}
func (s *Store) pingPreviewLocked(n *Node) []PingPreview {
	result := []PingPreview{}
	cutoff := time.Now().Unix() - 86400
	for _, target := range s.targetsLocked(n) {
		preview := PingPreview{Name: target.Name, Host: target.Host, Samples: make([]PingSample, 0, 24)}
		samples := n.PingHistory[target.Name]
		for i := len(samples) - 1; i >= 0; i-- {
			if samples[i].Host == target.Host {
				preview.Method = samples[i].Method
				break
			}
		}
		for i := len(samples) - 1; i >= 0 && len(preview.Samples) < 24; i-- {
			sample := samples[i]
			if sample.Timestamp < cutoff {
				break
			}
			if sample.Host == target.Host && sample.Method == preview.Method {
				preview.Samples = append(preview.Samples, sample)
			}
		}
		for left, right := 0, len(preview.Samples)-1; left < right; left, right = left+1, right-1 {
			preview.Samples[left], preview.Samples[right] = preview.Samples[right], preview.Samples[left]
		}
		if window := n.pingWindows[target.Name]; window != nil && window.Host == target.Host && window.Method == preview.Method {
			lost, total := window.counts(cutoff)
			if total > 0 {
				loss := math.Round(float64(lost)/float64(total)*1000) / 10
				preview.Loss = &loss
			}
		}
		result = append(result, preview)
	}
	return result
}
