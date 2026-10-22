package monitor

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowMetricCacheRefreshAndRetry(t *testing.T) {
	var cache metricCache[int]
	now := time.Now()
	calls, value, fail := 0, 10, false
	read := func() (int, error) {
		calls++
		if fail {
			return 0, errors.New("temporary read failure")
		}
		return value, nil
	}
	if got := cache.getAt(now, read); got != 10 || calls != 1 {
		t.Fatalf("first sample = %d, calls = %d", got, calls)
	}
	value = 20
	for second := 1; second < 5; second++ {
		if got := cache.getAt(now.Add(time.Duration(second)*time.Second), read); got != 10 || calls != 1 {
			t.Fatalf("cache expired too early: %d, %d", got, calls)
		}
	}
	fail = true
	if got := cache.getAt(now.Add(5*time.Second), read); got != 10 || calls != 2 {
		t.Fatalf("read error erased valid sample: %d, %d", got, calls)
	}
	fail = false
	if got := cache.getAt(now.Add(5500*time.Millisecond), read); got != 10 || calls != 2 {
		t.Fatalf("failed reads retried too frequently: %d, %d", got, calls)
	}
	if got := cache.getAt(now.Add(6*time.Second), read); got != 20 || calls != 3 {
		t.Fatalf("failed refresh waited five seconds: %d, %d", got, calls)
	}
}

func TestSlowMetricFirstFailureAndConcurrentReaders(t *testing.T) {
	var cache metricCache[int]
	now := time.Now()
	if got := cache.getAt(now, func() (int, error) { return 0, errors.New("not ready") }); got != 0 {
		t.Fatal(got)
	}
	var calls atomic.Int64
	read := func() (int, error) { calls.Add(1); return 42, nil }
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if got := cache.getAt(now.Add(time.Second), read); got != 42 {
				t.Errorf("first valid sample = %d", got)
			}
		}()
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent readers duplicated scan: %d", calls.Load())
	}
}
