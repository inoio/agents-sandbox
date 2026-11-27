package msb

import msbSdk "github.com/superradcompany/microsandbox/sdk/go"

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
