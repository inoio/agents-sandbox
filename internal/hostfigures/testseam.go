package hostfigures

// OverrideTotalMemoryGiBForTest replaces the installed-RAM read and returns a
// function that restores the previous one.
//
//deadcode:ignore reason="test seam: only tests and hostfigurestest's sentinel installer set it"
func OverrideTotalMemoryGiBForTest(read func() int) func() {
	previous := totalMemoryGiB
	totalMemoryGiB = read
	return func() { totalMemoryGiB = previous }
}

// OverrideNumCPUsForTest replaces the CPU-count read and returns a function that
// restores the previous one.
//
//deadcode:ignore reason="test seam: only tests and hostfigurestest's sentinel installer set it"
func OverrideNumCPUsForTest(read func() uint8) func() {
	previous := numCPUs
	numCPUs = read
	return func() { numCPUs = previous }
}

// OverrideAvailableMemoryMiBForTest replaces the available-memory read and
// returns a function that restores the previous one.
//
//deadcode:ignore reason="test seam: only tests and hostfigurestest's sentinel installer set it"
func OverrideAvailableMemoryMiBForTest(read func() (uint32, bool)) func() {
	previous := availableMemoryMiB
	availableMemoryMiB = read
	return func() { availableMemoryMiB = previous }
}
