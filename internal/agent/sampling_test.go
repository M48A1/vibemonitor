package agent

import (
	"context"
	"errors"
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

type cancelingSamplingCollector struct {
	calls        int
	cancel       context.CancelFunc
	cancelOnCall int
	err          error
}

func (c *cancelingSamplingCollector) GetBasicInfo() (protocol.BasicInfo, error) {
	return protocol.BasicInfo{}, nil
}

func (c *cancelingSamplingCollector) GetReport() (protocol.Report, error) {
	c.calls++
	if c.calls == c.cancelOnCall {
		c.cancel()
		return protocol.Report{Network: protocol.NetworkReport{Up: 99}}, c.err
	}
	return protocol.Report{}, nil
}

func TestSamplingCanceledEntryDoesNotCollectOrChangeQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	collector := &cancelingSamplingCollector{}
	reports := make(chan protocol.Report, 1)
	reports <- protocol.Report{Network: protocol.NetworkReport{Up: 7}}
	collectReports(ctx, collector, time.Millisecond, reports)
	if collector.calls != 0 || len(reports) != 1 || (<-reports).Network.Up != 7 {
		t.Fatal("canceled entry sampled or changed the pending report")
	}
}

func TestSamplingCancellationDuringCollectionKeepsPreviousReport(t *testing.T) {
	for _, test := range []struct {
		name string
		call int
		err  error
	}{
		{"baseline", 1, nil},
		{"sample", 2, nil},
		{"sample error", 2, errors.New("read failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			collector := &cancelingSamplingCollector{cancel: cancel, cancelOnCall: test.call, err: test.err}
			reports := make(chan protocol.Report, 1)
			reports <- protocol.Report{Network: protocol.NetworkReport{Up: 7}}
			collectReports(ctx, collector, time.Millisecond, reports)
			if collector.calls != test.call || len(reports) != 1 || (<-reports).Network.Up != 7 {
				t.Fatal("shutdown collected again, cleared the previous report, or published the canceled sample")
			}
		})
	}
}
