package msb

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// persistedConfigWithMounts mirrors the real persisted shape: SandboxConfig
// flattens SandboxSpec, whose mounts are an internally tagged VolumeMount array
// (microsandbox-types domain.rs). Note that stat_virtualization and
// host_permissions sit next to `options`, not inside it.
const persistedConfigWithMounts = `{
  "name": "agents-sandbox-vm-proj-opencode",
  "memory_mib": 16384,
  "mounts": [
    {"type":"Named","name":"agents-sandbox-home-proj-opencode-20261016T101010","guest":"/home/dev","options":{"readonly":false},"stat_virtualization":"strict","host_permissions":"private","follow_root_symlinks":false},
    {"type":"Bind","host":"/host/proj","guest":"/workspace","options":{"readonly":false},"stat_virtualization":"strict","host_permissions":"mirror","follow_root_symlinks":false,"quota_mib":16384},
    {"type":"Tmpfs","guest":"/tmp","size_mib":2048,"options":{}},
    {"type":"Owned","guest":"/data","storage":{"kind":"directory","quota_mib":512},"options":{},"stat_virtualization":"strict","host_permissions":"private"},
    {"type":"DiskImage","host":"/images/seed.img","guest":"/disk","format":"Raw","fstype":"ext4","options":{"readonly":true}}
  ]
}`

func TestParseConfigMountsMapsEveryVariant(t *testing.T) {
	got, err := ParseConfigMounts(persistedConfigWithMounts)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ParseConfigMounts() returned %d mounts, want 5: %+v", len(got), got)
	}

	home := got["/home/dev"]
	if home.Kind() != msbSdk.MountKindNamed || home.Named != "agents-sandbox-home-proj-opencode-20261016T101010" {
		t.Errorf("home mount = %+v, want named agents-sandbox-home-proj-opencode-20261016T101010", home)
	}

	workspace := got["/workspace"]
	if workspace.Kind() != msbSdk.MountKindBind || workspace.Bind != "/host/proj" || workspace.QuotaMiB != 16384 {
		t.Errorf("workspace mount = %+v, want bind /host/proj quota 16384", workspace)
	}
	if workspace.HostPermissions != msbSdk.HostPermissionsMirror {
		t.Errorf(
			"workspace mount host permissions = %q, want %q",
			workspace.HostPermissions,
			msbSdk.HostPermissionsMirror,
		)
	}

	tmp := got["/tmp"]
	if tmp.Kind() != msbSdk.MountKindTmpfs || tmp.SizeMiB != 2048 {
		t.Errorf("tmp mount = %+v, want tmpfs size 2048", tmp)
	}

	if owned := got["/data"]; owned.Kind() != msbSdk.MountKindOwned || owned.Owned != string(msbSdk.VolumeKindDir) {
		t.Errorf("owned mount = %+v, want owned kind dir", owned)
	}

	disk := got["/disk"]
	if disk.Kind() != msbSdk.MountKindDisk || disk.Disk != "/images/seed.img" || !disk.Readonly {
		t.Errorf("disk mount = %+v, want disk /images/seed.img read-only", disk)
	}
	// Upstream serializes DiskImageFormat without rename_all ("Raw"), while the
	// SDK's DiskOptions.Format hint is lowercase.
	if disk.Format != "raw" || disk.Fstype != "ext4" {
		t.Errorf("disk mount format/fstype = %q/%q, want raw/ext4", disk.Format, disk.Fstype)
	}
}

func TestParseConfigMountsWithoutMountsKeyIsNotAnError(t *testing.T) {
	got, err := ParseConfigMounts(`{"name":"vm","memory_mib":4096}`)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v, want nil for a config without mounts", err)
	}
	if len(got) != 0 || got == nil {
		t.Errorf("ParseConfigMounts() = %+v, want a non-nil empty map", got)
	}
}

func TestParseConfigMountsEmptyArrayIsAnEmptyMap(t *testing.T) {
	got, err := ParseConfigMounts(`{"mounts":[]}`)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 0 || got == nil {
		t.Errorf("ParseConfigMounts() = %+v, want a non-nil empty map", got)
	}
}

func TestParseConfigMountsSkipsUnknownType(t *testing.T) {
	raw := `{"mounts":[
	  {"type":"Tmpfs","guest":"/tmp","size_mib":2048},
	  {"type":"SomethingNew","guest":"/future"}
	]}`
	got, err := ParseConfigMounts(raw)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if len(got) != 1 || got["/tmp"].SizeMiB != 2048 {
		t.Errorf("ParseConfigMounts() = %+v, want only the recognized /tmp mount", got)
	}
}

func TestParseConfigMountsLastDuplicateGuestWins(t *testing.T) {
	raw := `{"mounts":[
	  {"type":"Tmpfs","guest":"/tmp","size_mib":2048},
	  {"type":"Tmpfs","guest":"/tmp","size_mib":4096}
	]}`
	got, err := ParseConfigMounts(raw)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	if got["/tmp"].SizeMiB != 4096 {
		t.Errorf("ParseConfigMounts()[/tmp] = %+v, want the last entry (4096)", got["/tmp"])
	}
}

func TestParseConfigMountsRejectsMalformedJSON(t *testing.T) {
	for _, configJSON := range []string{"not json", ""} {
		if _, err := ParseConfigMounts(configJSON); err == nil {
			t.Errorf("ParseConfigMounts(%q) error = nil, want an error for malformed JSON", configJSON)
		}
	}
}

func TestParseConfigMountsRejectsMountWithoutGuest(t *testing.T) {
	if _, err := ParseConfigMounts(`{"mounts":[{"type":"Tmpfs","size_mib":1}]}`); err == nil {
		t.Error("ParseConfigMounts() error = nil, want an error for a mount without a guest path")
	}
}

// TestParseConfigMountsOwnedDiskKind covers the disk branch of the Owned
// storage-kind mapping: upstream spells the directory kind "directory" while
// the SDK constant is "dir", so the disk spelling is easy to conflate or drop.
func TestParseConfigMountsOwnedDiskKind(t *testing.T) {
	raw := `{"mounts":[
	  {"type":"Owned","guest":"/data","storage":{"kind":"disk","capacity_mib":1024,"quota_mib":512},"options":{}}
	]}`
	got, err := ParseConfigMounts(raw)
	if err != nil {
		t.Fatalf("ParseConfigMounts() error = %v", err)
	}
	owned := got["/data"]
	if owned.Kind() != msbSdk.MountKindOwned || owned.Owned != string(msbSdk.VolumeKindDisk) {
		t.Errorf("owned mount = %+v, want owned kind disk", owned)
	}
	if owned.SizeMiB != 1024 || owned.QuotaMiB != 512 {
		t.Errorf("owned mount size/quota = %d/%d, want capacity_mib/quota_mib 1024/512", owned.SizeMiB, owned.QuotaMiB)
	}
}
