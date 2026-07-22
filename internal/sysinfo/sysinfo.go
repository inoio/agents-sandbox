package sysinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

func NumCPUs() uint8 {
	n := max(runtime.NumCPU(), 1)
	if n > 255 {
		n = 255
	}
	return uint8(n)
}

func parseMemInfo(data []byte) (totalKB int, ok bool) {
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

func TotalMemoryGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	if !ok {
		return 0
	}
	return totalKB / (1024 * 1024)
}
