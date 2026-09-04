//go:build !linux && !darwin

package monitor

import (
	"runtime"
	"time"

	"vibemonitor/pkg/protocol"
)

type OtherCollector struct {
	startTime time.Time
}

func NewCollector() Collector {
	return &OtherCollector{
		startTime: time.Now(),
	}
}

func (c *OtherCollector) GetBasicInfo() (protocol.BasicInfo, error) {
	return protocol.BasicInfo{
		Arch:     runtime.GOARCH,
		OS:       runtime.GOOS,
		CPUCores: runtime.NumCPU(),
		Version:  "1.0.0-lite",
	}, nil
}

func (c *OtherCollector) GetReport() (protocol.Report, error) {
	return protocol.Report{
		CPU: protocol.CPUReport{
			Cores: runtime.NumCPU(),
			Arch:  runtime.GOARCH,
			Usage: 10.0,
		},
		RAM: protocol.RAMReport{
			Total: 8 * 1024 * 1024 * 1024,
			Used:  4 * 1024 * 1024 * 1024,
		},
		Disk: protocol.DiskReport{
			Total: 100 * 1024 * 1024 * 1024,
			Used:  30 * 1024 * 1024 * 1024,
		},
		Uptime:    int64(time.Since(c.startTime).Seconds()),
		UpdatedAt: time.Now().UTC(),
	}, nil
}
