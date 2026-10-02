//go:build linux && (amd64 || arm64)

package monitor

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vibemonitor/internal/version"
	"vibemonitor/pkg/protocol"
)

type LinuxCollector struct {
	cpuTracker CPUTracker
	netTracker NetTracker
	tcpTracker tcpConnectionRateTracker
	startTime  time.Time
	interfaces map[string]bool
	diskCache  metricCache[protocol.DiskReport]
	connCache  metricCache[connectionCounts]
	procCache  metricCache[int]
}

type connectionCounts struct{ tcp, udp int }

func NewCollector(interfaces ...string) Collector {
	selected := make(map[string]bool)
	for _, name := range interfaces {
		if name = strings.TrimSpace(name); name != "" {
			selected[name] = true
		}
	}
	return &LinuxCollector{
		startTime:  time.Now(),
		interfaces: selected,
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
	if err := syscall.Statfs(getDiskPath(), &stat); err == nil {
		info.DiskTotal = int64(stat.Blocks * uint64(stat.Bsize))
	}

	return info, nil
}

func getDiskPath() string {
	if p := os.Getenv("VIBEMONITOR_DISK_PATH"); p != "" {
		return p
	}
	return "/"
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

	// 4. Disk: filesystem statistics change slowly and can be expensive.
	report.Disk = c.diskCache.getAt(time.Now(), func() (protocol.DiskReport, error) {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(getDiskPath(), &stat); err != nil {
			return protocol.DiskReport{}, err
		}
		total := int64(stat.Blocks * uint64(stat.Bsize))
		free := int64(stat.Bavail * uint64(stat.Bsize))
		if free > total {
			free = total
		}
		return protocol.DiskReport{Total: total, Used: total - free}, nil
	})

	// 5. Network
	totalDown, totalUp, source, counters, err := c.readNetDev()
	sampledAt := time.Now()
	if err != nil {
		return protocol.Report{}, fmt.Errorf("network counters: %w", err)
	}
	report.UpdatedAt = sampledAt.UTC()
	upSpeed, downSpeed := c.netTracker.CalculateInterfaceSpeedAt(counters, sampledAt)
	report.Network.Up = upSpeed
	report.Network.Down = downSpeed
	report.Network.TotalUp = totalUp
	report.Network.TotalDown = totalDown
	report.Network.Source = source
	report.Network.BootID = readKernelBootID("/proc/sys/kernel/random/boot_id")
	report.Network.Interfaces = counters

	// 6. TCP attempts/s stays on the fast path; socket and process counts
	// scan potentially large tables and are refreshed every five seconds.
	if data, err := os.ReadFile("/proc/net/snmp"); err == nil {
		if active, passive, err := parseTCPConnectionCounters(data); err == nil {
			report.Connections.TCPNewPerSecond = c.tcpTracker.CalculateRateAt(active, passive, time.Now())
		} else {
			c.tcpTracker.Reset()
		}
	} else {
		c.tcpTracker.Reset()
	}
	counts := c.connCache.getAt(time.Now(), readConnectionCounts)
	report.Connections.TCP, report.Connections.UDP = counts.tcp, counts.udp
	report.Process = c.procCache.getAt(time.Now(), countProcesses)

	// 7. Uptime
	report.Uptime = readUptime()

	return report, nil
}

func readCPUStats() (total, idle uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}

	return parseCPUStats(data)
}

func readMemInfo() (memTotal, memUsed, swapTotal, swapUsed int64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return parseMemInfo(data)
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

func (c *LinuxCollector) readNetDev() (totalDown, totalUp int64, source string, counters map[string]protocol.InterfaceCounters, err error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, "", nil, err
	}
	seen := make(map[string]bool)
	totalDown, totalUp, source, counters, err = parseNetworkCountersDetailed(data, func(name string) bool {
		if len(c.interfaces) > 0 {
			if c.interfaces[name] {
				seen[name] = true
			}
			return c.interfaces[name]
		}
		return autoTrafficInterface("/sys/class/net", name)
	})
	if err != nil {
		return 0, 0, "", nil, err
	}
	for name := range c.interfaces {
		if !seen[name] {
			return 0, 0, "", nil, fmt.Errorf("traffic interface %q not found", name)
		}
	}
	return totalDown, totalUp, source, counters, nil
}

func readKernelBootID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Prefer the carrier interfaces: stacked devices can report the same bytes
// already counted by the physical link. Explicit --interfaces overrides this.
func autoTrafficInterface(sysRoot, name string) bool {
	if !defaultTrafficInterface(name) || strings.Contains(name, ".") {
		return false
	}
	device := filepath.Join(sysRoot, name)
	for _, child := range []string{"bridge", "bonding"} {
		if _, err := os.Stat(filepath.Join(device, child)); err == nil {
			return false
		}
	}
	if entries, err := os.ReadDir(device); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "lower_") {
				return false
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(device, "uevent")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			switch line {
			case "DEVTYPE=bridge", "DEVTYPE=bond", "DEVTYPE=vlan", "DEVTYPE=macvlan", "DEVTYPE=vxlan", "DEVTYPE=geneve":
				return false
			}
		}
	}
	// A physical bridge or bond port is the carrier, while a virtual port
	// repeats traffic seen on another link.
	if _, err := os.Stat(filepath.Join(device, "device")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(device, "brport")); err == nil {
		return false
	}
	if kind, err := os.ReadFile(filepath.Join(device, "type")); err == nil && strings.TrimSpace(string(kind)) != "1" {
		return false
	}
	return true
}

var lineBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 8192)
		return &b
	},
}

func readConnectionCounts() (connectionCounts, error) {
	var counts connectionCounts
	for _, table := range []struct {
		path  string
		total *int
	}{
		{"/proc/net/tcp", &counts.tcp}, {"/proc/net/tcp6", &counts.tcp},
		{"/proc/net/udp", &counts.udp}, {"/proc/net/udp6", &counts.udp},
	} {
		count, err := countLinesInFile(table.path)
		// IPv6 socket tables are absent when the kernel disables IPv6.
		if os.IsNotExist(err) && strings.HasSuffix(table.path, "6") {
			continue
		}
		if err != nil {
			return connectionCounts{}, err
		}
		*table.total += count
	}
	return counts, nil
}

func countLinesInFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	bufPtr := lineBufPool.Get().(*[]byte)
	defer lineBufPool.Put(bufPtr)
	buf := *bufPtr

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
			if err != io.EOF {
				return 0, err
			}
			break
		}
	}

	if !hasData {
		return 0, nil
	}
	if !lastByteWasLF {
		lineCount++
	}
	if lineCount <= 1 {
		return 0, nil
	}
	return lineCount - 1, nil // subtract header
}

func countProcesses() (int, error) {
	f, err := os.Open("/proc")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	for {
		names, err := f.Readdirnames(128)
		for _, name := range names {
			if isAllDigits(name) {
				count++
			}
		}
		if err != nil {
			if err != io.EOF {
				return 0, err
			}
			break
		}
	}
	return count, nil
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
