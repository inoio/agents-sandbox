package sysinfo

import (
	"runtime"
	"strconv"
	"strings"
)

const (
	maxCPUs = 255
	kiB     = 1024
)

// clampNumCPUs bounds a reported CPU count to the range NumCPUs exposes: at
// least 1 (a running host always has a CPU) and at most maxCPUs (the count
// travels as a uint8). The count is a parameter so the bounds stay testable.
func clampNumCPUs(cpus int) uint8 {
	return uint8(min(max(cpus, 1), maxCPUs))
}

// NumCPUs reports the host's logical CPU count, clamped to [1, maxCPUs].
func NumCPUs() uint8 {
	return clampNumCPUs(runtime.NumCPU())
}

func parseMemInfo(data []byte) (int, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}
