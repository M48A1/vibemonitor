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
	Targets      []protocol.PingTarget `json:"targets"`
	DueDate      string                `json:"due_date"`
	PaymentCycle string                `json:"payment_cycle"`
	Price        float64               `json:"price"`
	Currency     string                `json:"currency"`
}
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
	for _, target := range s.targetsLocked(n) {
		preview := PingPreview{Name: target.Name, Host: target.Host, Samples: []PingSample{}}
		samples := n.PingHistory[target.Name]
		for i := len(samples) - 1; i >= 0; i-- {
			if samples[i].Host == target.Host {
				preview.Method = samples[i].Method
				break
			}
		}
		lost, total := 0, 0
		for _, sample := range samples {
			if sample.Host != target.Host || sample.Method != preview.Method || sample.Timestamp < time.Now().Unix()-86400 {
				continue
			}
			total++
			if sample.Latency < 0 {
				lost++
			}
			preview.Samples = append(preview.Samples, sample)
		}
		if total > 0 {
			loss := math.Round(float64(lost)/float64(total)*1000) / 10
			preview.Loss = &loss
		}
		if len(preview.Samples) > 24 {
			preview.Samples = preview.Samples[len(preview.Samples)-24:]
		}
		result = append(result, preview)
	}
	return result
}
