//go:build linux && (amd64 || arm64)

package agent

import (
	"context"
	"testing"

	"vibemonitor/pkg/protocol"
)

type cancelingBasicCollector struct {
	basicCalls, reportCalls int
	cancel                  context.CancelFunc
}

func (c *cancelingBasicCollector) GetBasicInfo() (protocol.BasicInfo, error) {
	c.basicCalls++
	if c.cancel != nil {
		c.cancel()
	}
	return protocol.BasicInfo{}, nil
}

func (c *cancelingBasicCollector) GetReport() (protocol.Report, error) {
	c.reportCalls++
	return protocol.Report{}, nil
}

func TestClientCanceledEntrySkipsBasicInfoAndWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	collector := &cancelingBasicCollector{}
	client := &Client{collector: collector}
	if err := client.Run(ctx); err != nil || collector.basicCalls != 0 || collector.reportCalls != 0 {
		t.Fatalf("canceled entry: basic=%d reports=%d err=%v", collector.basicCalls, collector.reportCalls, err)
	}
}

func TestClientCanceledDuringBasicInfoSkipsRPCAndWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector := &cancelingBasicCollector{cancel: cancel}
	// No HTTP client is installed: reaching RPC would panic, and no network is used.
	client := &Client{collector: collector}
	if err := client.Run(ctx); err != nil || collector.basicCalls != 1 || collector.reportCalls != 0 {
		t.Fatalf("canceled basic info: basic=%d reports=%d err=%v", collector.basicCalls, collector.reportCalls, err)
	}
}
