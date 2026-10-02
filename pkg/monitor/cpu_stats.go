package monitor

import (
	"bytes"
	"fmt"
	"strconv"
)

// parseCPUStats sums user through steal. Linux already includes guest and
// guest_nice in user and nice, so the final two fields must not be added again.
func parseCPUStats(data []byte) (total, idle uint64, err error) {
	for len(data) > 0 {
		line, remaining, _ := bytes.Cut(data, []byte{'\n'})
		data = remaining
		fields := bytes.Fields(line)
		if len(fields) == 0 || !bytes.Equal(fields[0], []byte("cpu")) {
			continue
		}
		if len(fields) < 5 {
			return 0, 0, fmt.Errorf("invalid cpu format")
		}
		end := min(len(fields), 9)
		for i := 1; i < end; i++ {
			value, parseErr := strconv.ParseUint(string(fields[i]), 10, 64)
			if parseErr != nil || value > ^uint64(0)-total {
				return 0, 0, fmt.Errorf("invalid cpu counter")
			}
			total += value
			if i == 4 || i == 5 {
				idle += value
			}
		}
		return total, idle, nil
	}
	return 0, 0, fmt.Errorf("cpu line not found")
}
