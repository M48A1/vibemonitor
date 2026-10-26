//go:build linux && (amd64 || arm64)

package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestMeasurePingsStampsCompletion(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	c := &Client{pingTargets: []protocol.PingTarget{{Name: "local", Host: listener.Addr().String()}}}
	before := time.Now().Unix()
	c.measurePings(context.Background())
	if len(c.pingResults) != 1 { t.Fatal("missing probe result") }
	result := c.pingResults[0]
	if result.MeasuredAt < before || result.MeasuredAt > time.Now().Unix() || result.Latency < 0 {
		t.Fatalf("invalid completed probe: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.measurePings(ctx)
	if c.pingResults[0] != result { t.Fatal("canceled cycle replaced completed measurement") }
}
