package msbtest_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestRoleDoublesSatisfyTheirRoles pins each standalone role double to its
// role interface at compile time. A double that drifts from its role's
// signatures fails this test.
func TestRoleDoublesSatisfyTheirRoles(t *testing.T) {
	var _ msb.RuntimeInstaller = (*msbtest.MockRuntimeInstaller)(nil)
	var _ msb.SandboxStore = (*msbtest.MockSandboxStore)(nil)
	var _ msb.VolumeStore = (*msbtest.MockVolumeStore)(nil)
	var _ msb.ImageStore = (*msbtest.MockImageStore)(nil)
	var _ msb.StorageStore = (*msbtest.MockStorageStore)(nil)
	var _ msb.SandboxExecutor = (*msbtest.MockSandboxExecutor)(nil)
	var _ msb.SandboxStreamer = (*msbtest.MockSandboxStreamer)(nil)
	var _ msb.SandboxAttacher = (*msbtest.MockSandboxAttacher)(nil)
	var _ msb.SandboxJobs = (*msbtest.MockSandboxJobs)(nil)
	var _ msb.SandboxObserver = (*msbtest.MockSandboxObserver)(nil)
}
