package monitor

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// parseMemInfo distinguishes an available-memory value of zero from an absent
// field on older kernels. A present zero means all memory is in use.
func parseMemInfo(data []byte) (memTotal, memUsed, swapTotal, swapUsed int64, err error) {
	var (
		total, avail, free, buffers, cached, swapTot, swapFr int64
	)
	hasAvailable := false
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
			hasAvailable = true
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
	if hasAvailable {
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
