package msb

import (
	"reflect"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Two-way direct assignment (no conversion) compiles only while the launcher
// name is an alias of the SDK type. A defined type fails to build here.
var (
	_ ImageConfig             = msbSdk.ImageConfig{}
	_ msbSdk.ImageConfig      = ImageConfig{}
	_ ImageLayer              = msbSdk.ImageLayer{}
	_ msbSdk.ImageLayer       = ImageLayer{}
	_ ImagePruneReport        = msbSdk.ImagePruneReport{}
	_ msbSdk.ImagePruneReport = ImagePruneReport{}
)

var (
	_ StorageReport               = msbSdk.StorageUsageReport{}
	_ msbSdk.StorageUsageReport   = StorageReport{}
	_ StorageCategory             = msbSdk.StorageCategoryUsage{}
	_ msbSdk.StorageCategoryUsage = StorageCategory{}
	_ StorageItem                 = msbSdk.StorageItemUsage{}
	_ msbSdk.StorageItemUsage     = StorageItem{}
)

var (
	_ Metrics        = msbSdk.Metrics{}
	_ msbSdk.Metrics = Metrics{}
)

var (
	_ ExecFailure        = msbSdk.ExecFailure{}
	_ msbSdk.ExecFailure = ExecFailure{}
)

var (
	_ DiskCompactionDiskResult        = msbSdk.DiskCompactionDiskResult{}
	_ msbSdk.DiskCompactionDiskResult = DiskCompactionDiskResult{}
	_ ModificationConflict            = msbSdk.ModificationConflict{}
	_ msbSdk.ModificationConflict     = ModificationConflict{}
)

// TestAliasSetIsExactlyTheObservations pins exclusivity: an aliased observation
// resolves to the SDK package, and every type the port keeps launcher-owned
// resolves to this package. A defined type in the alias set, or an accidental
// alias of a kept type, fails here. The two-way assignments above cannot catch
// the latter, because a string-backed SDK enum still converts.
func TestAliasSetIsExactlyTheObservations(t *testing.T) {
	launcherPackage := reflect.TypeFor[VMStatusKind]().PkgPath()

	aliases := map[string]any{
		"ImageConfig":              ImageConfig{},
		"ImageLayer":               ImageLayer{},
		"ImagePruneReport":         ImagePruneReport{},
		"StorageReport":            StorageReport{},
		"StorageCategory":          StorageCategory{},
		"StorageItem":              StorageItem{},
		"Metrics":                  Metrics{},
		"ExecFailure":              ExecFailure{},
		"DiskCompactionDiskResult": DiskCompactionDiskResult{},
		"ModificationConflict":     ModificationConflict{},
	}
	for name, value := range aliases {
		if got := reflect.TypeOf(value).PkgPath(); got == launcherPackage {
			t.Errorf("%s resolves to %q, want the SDK package (it must be an alias)", name, got)
		}
	}

	launcherOwned := map[string]any{
		"JobInfo":                 JobInfo{},
		"JobLogEntry":             JobLogEntry{},
		"LogEntry":                LogEntry{},
		"SandboxModificationPlan": SandboxModificationPlan{},
		"PruneReport":             PruneReport{},
		"PruneEntry":              PruneEntry{},
		"DiskCompactionResult":    DiskCompactionResult{},
		"ExecRequest":             ExecRequest{},
		"JobSpec":                 JobSpec{},
		"SandboxConfig":           SandboxConfig{},
		"MountConfig":             MountConfig{},
		"PortBinding":             PortBinding{},
		"SandboxStatus":           SandboxStatus(""),
		"LogSource":               LogSource(""),
	}
	for name, value := range launcherOwned {
		if got := reflect.TypeOf(value).PkgPath(); got != launcherPackage {
			t.Errorf("%s resolves to %q, want %q (it must stay launcher-owned)", name, got, launcherPackage)
		}
	}
}
