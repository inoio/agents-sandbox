package state

import "github.com/inoio/agents-sandbox/internal/atomicwrite"

// writeStateFile is the durable writer the root-state funnel uses. Tests
// substitute it to force a write failure without permission bits, which an
// effective uid of 0 bypasses.
var writeStateFile = atomicwrite.WriteFile

// OverrideStateWriteForTest replaces the writer the root-state funnel uses and
// returns a restore function. Tests use it to force a write failure
// deterministically, without permission bits an effective uid of 0 bypasses.
//
//deadcode:ignore reason="test seam: only tests set it"
func OverrideStateWriteForTest(write func(path string, data []byte) error) func() {
	previous := writeStateFile
	writeStateFile = write
	return func() { writeStateFile = previous }
}
