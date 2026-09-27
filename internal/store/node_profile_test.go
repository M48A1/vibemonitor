package store

import (
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

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
