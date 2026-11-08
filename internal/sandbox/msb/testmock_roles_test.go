package msb

import "testing"

// TestRoleDoublesSatisfyTheirRoles pins each standalone role double to its
// role interface at compile time. A double that drifts from its role's
// signatures fails this test.
func TestRoleDoublesSatisfyTheirRoles(t *testing.T) {
	var _ RuntimeInstaller = (*MockRuntimeInstaller)(nil)
	var _ SandboxStore = (*MockSandboxStore)(nil)
	var _ VolumeStore = (*MockVolumeStore)(nil)
	var _ ImageStore = (*MockImageStore)(nil)
	var _ StorageStore = (*MockStorageStore)(nil)
	var _ SandboxExecutor = (*MockSandboxExecutor)(nil)
	var _ SandboxStreamer = (*MockSandboxStreamer)(nil)
	var _ SandboxAttacher = (*MockSandboxAttacher)(nil)
	var _ SandboxJobs = (*MockSandboxJobs)(nil)
	var _ SandboxObserver = (*MockSandboxObserver)(nil)
}
