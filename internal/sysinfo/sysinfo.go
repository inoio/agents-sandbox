package sysinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	maxCPUs   = 255
	minFields = 2
	kiB       = 1024
)

func NumCPUs() uint8 {
	n := min(max(runtime.NumCPU(), 1), maxCPUs)
	return uint8(n) //nolint:gosec // G115: n is bounded by min(_, maxCPUs) where maxCPUs=255
}

func parseMemInfo(data []byte) (int, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < minFields {
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

func TotalMemoryGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}
