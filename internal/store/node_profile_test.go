package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestNodeCPUThresholdPersistsAndValidates(t *testing.T) {
	for _, value := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		if err := validateProfile(&NodeProfile{CPUThreshold: &value}); err == nil {
			t.Fatalf("accepted invalid CPU threshold %v", value)
		}
	}
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	threshold := 85.5
	node, err := s.CreateNodeWithOptions(NodeOptions{Name: "threshold", Profile: &NodeProfile{CPUThreshold: &threshold}})
	if err != nil {
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
	got := s.GetNodes()[0].Profile
	if got == nil || got.CPUThreshold == nil || *got.CPUThreshold != threshold {
		t.Fatalf("CPU threshold lost after restart: %+v", got)
	}
	if err := s.UpdateNodeWithOptions(node.UUID, NodeOptions{Profile: &NodeProfile{}}); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNodes()[0].Profile; got == nil || got.CPUThreshold != nil {
		t.Fatalf("CPU threshold not cleared: %+v", got)
	}
}

func TestPingPreviewKeepsLatestSamplesAndFullDayLoss(t *testing.T) {
	now := time.Now().Unix()
	const host = "example.com:443"
	samples := []PingSample{{Host: host, Method: "tcp", Timestamp: now - 90000, Latency: -1}}
	for i := 0; i < 30; i++ {
		latency := 50
		if i%10 == 0 {
			latency = -1
		}
		samples = append(samples, PingSample{Host: host, Method: "tcp", Timestamp: now - 3600 + int64(i*60), Latency: latency})
	}
	samples = append(samples, PingSample{Host: "other.example:443", Method: "tcp", Timestamp: now - 1, Latency: -1})
	node := &Node{
		Profile:     &NodeProfile{Targets: []protocol.PingTarget{{Name: "target", Host: host}}},
		PingHistory: map[string][]PingSample{"target": samples},
	}

	preview := (&Store{}).pingPreviewLocked(node)
	if len(preview) != 1 || preview[0].Loss == nil || *preview[0].Loss != 10 {
		t.Fatalf("unexpected preview loss: %+v", preview)
	}
	if got := preview[0].Samples; len(got) != 24 || got[0].Timestamp != samples[7].Timestamp || got[23].Timestamp != samples[30].Timestamp {
		t.Fatalf("unexpected latest samples: %+v", got)
	}
}
