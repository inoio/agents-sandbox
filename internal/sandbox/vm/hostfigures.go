package vm

import "github.com/inoio/agents-sandbox/internal/sysinfo"

// The host figures a project VM's defaults derive from. They are package vars
// so a test binary can pin them to a fixed machine instead of the one it runs
// on; production reads the machine through the sysinfo defaults.
var (
	totalMemoryGiB = sysinfo.TotalMemoryGiB
	numCPUs        = sysinfo.NumCPUs
)
