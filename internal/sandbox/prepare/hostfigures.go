package prepare

import "github.com/inoio/agents-sandbox/internal/sysinfo"

// hostAvailableMemoryMiB is the host's available-memory read the memory-raise
// offer derives from. It is a package var so a test binary can pin it to a
// fixed machine instead of the one it runs on; production reads the machine
// through the sysinfo default.
var hostAvailableMemoryMiB = sysinfo.AvailableMemoryMiB
