package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

type samplingCollector struct {
	calls   atomic.Int64
	sampled chan int64
}

func (c *samplingCollector) GetBasicInfo() (protocol.BasicInfo, error) {
	return protocol.BasicInfo{}, nil
}

func (c *samplingCollector) GetReport() (protocol.Report, error) {
	n := c.calls.Add(1)
	c.sampled <- n
	return protocol.Report{Network: protocol.NetworkReport{Up: n, Down: n * 2}}, nil
}

func TestSamplingContinuesWithoutUploaderAndKeepsLatest(t *testing.T) {
	c := &samplingCollector{sampled: make(chan int64, 100)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := make(chan protocol.Report, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		collectReports(ctx, c, time.Millisecond, reports)
	}()
	// Leave the upload queue unread while multiple samples are collected.
	for i := 0; i < 5; i++ {
		select {
		case <-c.sampled:
		case <-time.After(time.Second):
			t.Fatal("sampling blocked by an unread upload queue")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sampling did not stop")
	}
	report := <-reports
	if report.Network.Up < 4 || report.Network.Down != report.Network.Up*2 {
		t.Fatalf("expected latest independent rates, got %+v", report.Network)
	}
	if len(reports) != 0 {
		t.Fatal("stale reports accumulated")
	}
}
