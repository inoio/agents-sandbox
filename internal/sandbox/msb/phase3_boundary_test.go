package msb

import (
	"go/build"
	"testing"
)

// TestPhase3ConsumersDoNotImportSDK pins the phase-3 boundary: the packages
// whose msb-facing return values and lifecycle enums are now launcher-owned
// must not import the microsandbox SDK in their non-test sources. vm, volume
// and pruning still import it for the phase-4 config model (SandboxConfig,
// MountConfig, VolumeSpec, DiskCompaction*) and are deliberately not covered.
func TestPhase3ConsumersDoNotImportSDK(t *testing.T) {
	const sdkPath = "github.com/superradcompany/microsandbox/sdk/go"
	for _, dir := range []string{
		"../storage",
		"../pressure",
		"../resourceproposal",
		"../image",
		"../session",
	} {
		pkg, err := build.ImportDir(dir, 0)
		if err != nil {
			t.Fatalf("import dir %s: %v", dir, err)
		}
		for _, imported := range pkg.Imports {
			if imported == sdkPath {
				t.Errorf(
					"%s imports the microsandbox SDK; its msb-facing values are launcher-owned (phase 3)",
					dir,
				)
			}
		}
	}
}
