//go:build linux && amd64

package monitor

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vibemonitor/internal/version"
	"vibemonitor/pkg/protocol"
)

type LinuxCollector struct {
	cpuTracker CPUTracker
	netTracker NetTracker
	startTime  time.Time
}

func NewCollector() Collector {
	return &LinuxCollector{
		startTime: time.Now(),
	}
}

func (c *LinuxCollector) GetBasicInfo() (protocol.BasicInfo, error) {
	info := protocol.BasicInfo{
		Arch:     runtime.GOARCH,
		OS:       "Linux",
		CPUCores: runtime.NumCPU(),
		Version:  version.Version,
	}

	// Read /etc/os-release
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}

	// Read /proc/version for kernel version
	if data, err := os.ReadFile("/proc/version"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			info.KernelVersion = fields[2]
		}
	}

	// Read /proc/cpuinfo
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.CPUName = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// Memory total
	if memTotal, _, _, _, err := readMemInfo(); err == nil {
		info.MemTotal = memTotal
	}

	// Disk total
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		info.DiskTotal = int64(stat.Blocks * uint64(stat.Bsize))
	}

	return info, nil
}

func (c *LinuxCollector) GetReport() (protocol.Report, error) {
	report := protocol.Report{
		UpdatedAt: time.Now().UTC(),
	}

	// 1. CPU Usage
	total, idle, err := readCPUStats()
	if err == nil {
		report.CPU.Usage = c.cpuTracker.CalculateUsage(total, idle)
	}
	report.CPU.Cores = runtime.NumCPU()
	report.CPU.Arch = runtime.GOARCH

	// 2. Memory & Swap
	memTotal, memUsed, swapTotal, swapUsed, err := readMemInfo()
	if err == nil {
		report.RAM.Total = memTotal
		report.RAM.Used = memUsed
		report.Swap.Total = swapTotal
		report.Swap.Used = swapUsed
	}

	// 3. Load
	report.Load = readLoadAvg()

	// 4. Disk
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		total := int64(stat.Blocks * uint64(stat.Bsize))
		free := int64(stat.Bfree * uint64(stat.Bsize))
		report.Disk.Total = total
		report.Disk.Used = total - free
	}

	// 5. Network
	totalDown, totalUp, err := readNetDev()
	if err == nil {
		upSpeed, downSpeed := c.netTracker.CalculateSpeed(totalUp, totalDown)
		report.Network.Up = upSpeed
		report.Network.Down = downSpeed
		report.Network.TotalUp = totalUp
		report.Network.TotalDown = totalDown
	}

	// 6. Connections & Process
	report.Connections.TCP = countLinesInFile("/proc/net/tcp") + countLinesInFile("/proc/net/tcp6")
	report.Connections.UDP = countLinesInFile("/proc/net/udp") + countLinesInFile("/proc/net/udp6")
	report.Process = countProcesses()

	// 7. Uptime
	report.Uptime = readUptime()

	return report, nil
}

func readCPUStats() (total, idle uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}

	// Fast-path: The aggregated "cpu " metric is always on the first line
	var firstLine string
	if idx := bytes.IndexByte(data, '\n'); idx != -1 {
		firstLine = string(data[:idx])
	} else {
		firstLine = string(data)
	}

	if strings.HasPrefix(firstLine, "cpu ") {
		fields := strings.Fields(firstLine)
		if len(fields) < 5 {
			return 0, 0, fmt.Errorf("invalid cpu format")
		}
		var sum uint64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			sum += v
		}
		idleVal, _ := strconv.ParseUint(fields[4], 10, 64)
		var iowaitVal uint64
		if len(fields) >= 6 {
			iowaitVal, _ = strconv.ParseUint(fields[5], 10, 64)
		}
		return sum, idleVal + iowaitVal, nil
	}

	// Fallback in case "cpu " is not the very first line
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return 0, 0, fmt.Errorf("invalid cpu format")
			}
			var sum uint64
			for i := 1; i < len(fields); i++ {
				v, _ := strconv.ParseUint(fields[i], 10, 64)
				sum += v
			}
			idleVal, _ := strconv.ParseUint(fields[4], 10, 64)
			var iowaitVal uint64
			if len(fields) >= 6 {
				iowaitVal, _ = strconv.ParseUint(fields[5], 10, 64)
			}
			return sum, idleVal + iowaitVal, nil
		}
	}
	return 0, 0, fmt.Errorf("cpu line not found")
}

func readMemInfo() (memTotal, memUsed, swapTotal, swapUsed int64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	var (
		total, avail, free, buffers, cached, swapTot, swapFr int64
	)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valFields := strings.Fields(parts[1])
		if len(valFields) == 0 {
			continue
		}
		valKb, _ := strconv.ParseInt(valFields[0], 10, 64)
		valBytes := valKb * 1024

		switch key {
		case "MemTotal":
			total = valBytes
		case "MemAvailable":
			avail = valBytes
		case "MemFree":
			free = valBytes
		case "Buffers":
			buffers = valBytes
		case "Cached":
			cached = valBytes
		case "SwapTotal":
			swapTot = valBytes
		case "SwapFree":
			swapFr = valBytes
		}
	}

	memTotal = total
	if avail > 0 {
		memUsed = total - avail
	} else {
		memUsed = total - free - buffers - cached
	}
	if memUsed < 0 {
		memUsed = 0
	}

	swapTotal = swapTot
	swapUsed = swapTot - swapFr
	if swapUsed < 0 {
		swapUsed = 0
	}
	return memTotal, memUsed, swapTotal, swapUsed, nil
}

func readLoadAvg() protocol.LoadReport {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return protocol.LoadReport{}
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return protocol.LoadReport{}
	}
	l1, _ := strconv.ParseFloat(fields[0], 64)
	l5, _ := strconv.ParseFloat(fields[1], 64)
	l15, _ := strconv.ParseFloat(fields[2], 64)
	return protocol.LoadReport{
		Load1:  l1,
		Load5:  l5,
		Load15: l15,
	}
}

func readNetDev() (totalDown, totalUp int64, err error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "veth") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) >= 9 {
			rx, _ := strconv.ParseInt(fields[0], 10, 64)
			tx, _ := strconv.ParseInt(fields[8], 10, 64)
			totalDown += rx
			totalUp += tx
		}
	}
	return totalDown, totalUp, nil
}

func countLinesInFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	buf := make([]byte, 8192)
	lineCount := 0
	hasData := false
	lastByteWasLF := false

	for {
		n, err := f.Read(buf)
		if n > 0 {
			hasData = true
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					lineCount++
				}
			}
			lastByteWasLF = (buf[n-1] == '\n')
		}
		if err != nil {
			break
		}
	}

	if !hasData {
		return 0
	}
	if !lastByteWasLF {
		lineCount++
	}
	if lineCount <= 1 {
		return 0
	}
	return lineCount - 1 // subtract header
}

func countProcesses() int {
	f, err := os.Open("/proc")
	if err != nil {
		return 0
	}
	defer f.Close()

	names, err := f.Readdirnames(-1)
	if err != nil {
		return 0
	}
	count := 0
	for _, name := range names {
		if isAllDigits(name) {
			count++
		}
	}
	return count
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		secs, _ := strconv.ParseFloat(fields[0], 64)
		return int64(secs)
	}
	return 0
}
