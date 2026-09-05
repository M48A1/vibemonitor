package monitor

import (
	"sync"
	"time"

	"vibemonitor/pkg/protocol"
)

type Collector interface {
	GetBasicInfo() (protocol.BasicInfo, error)
	GetReport() (protocol.Report, error)
}

// NetTracker tracks cumulative bytes to calculate real-time speeds (bytes/sec)
type NetTracker struct {
	mu            sync.Mutex
	lastTime      time.Time
	lastTotalUp   int64
	lastTotalDown int64
}

func (nt *NetTracker) CalculateSpeed(curUp, curDown int64) (upSpeed, downSpeed int64) {
	nt.mu.Lock()
	defer nt.mu.Unlock()

	now := time.Now()
	if nt.lastTime.IsZero() {
		nt.lastTime = now
		nt.lastTotalUp = curUp
		nt.lastTotalDown = curDown
		return 0, 0
	}

	elapsed := now.Sub(nt.lastTime).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}

	if curUp >= nt.lastTotalUp {
		upSpeed = int64(float64(curUp-nt.lastTotalUp) / elapsed)
	}
	if curDown >= nt.lastTotalDown {
		downSpeed = int64(float64(curDown-nt.lastTotalDown) / elapsed)
	}

	nt.lastTime = now
	nt.lastTotalUp = curUp
	nt.lastTotalDown = curDown
	return upSpeed, downSpeed
}

// CPUTracker tracks CPU ticks to calculate usage percentage
type CPUTracker struct {
	mu        sync.Mutex
	lastTime  time.Time
	lastTotal uint64
	lastIdle  uint64
}

func (ct *CPUTracker) CalculateUsage(total, idle uint64) float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.lastTotal == 0 {
		ct.lastTotal = total
		ct.lastIdle = idle
		ct.lastTime = time.Now()
		return 0.0
	}

	deltaTotal := float64(total - ct.lastTotal)
	deltaIdle := float64(idle - ct.lastIdle)

	ct.lastTotal = total
	ct.lastIdle = idle
	ct.lastTime = time.Now()

	if deltaTotal <= 0 {
		return 0.0
	}
	usage := (1.0 - (deltaIdle / deltaTotal)) * 100.0
	if usage < 0 {
		usage = 0
	} else if usage > 100 {
		usage = 100
	}
	return usage
}
