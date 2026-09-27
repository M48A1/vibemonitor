//go:build linux && (amd64 || arm64)

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemPingStopsWithParentContext(t *testing.T) {
	pingPath := filepath.Join(t.TempDir(), "ping")
	if err := os.WriteFile(pingPath, []byte("#!/bin/sh\nprintf ready > \"$PING_STARTED\"\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	startedPath := filepath.Join(t.TempDir(), "started")
	t.Setenv("PATH", filepath.Dir(pingPath)+":"+os.Getenv("PATH"))
	t.Setenv("PING_STARTED", startedPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- execSystemPing(ctx, "example.com", 10*time.Second) }()
	deadline := time.After(time.Second)
	for {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("ping process did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case result := <-done:
		if result != -1 {
			t.Fatalf("canceled ping returned %d", result)
		}
	case <-time.After(time.Second):
		t.Fatal("ping process ignored parent cancellation")
	}
}
