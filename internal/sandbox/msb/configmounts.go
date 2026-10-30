package msb

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// persistedConfigMounts mirrors the `mounts` array of a persisted sandbox
// config. The runtime stores a SandboxSpec whose mounts are an internally tagged
// VolumeMount array, while the Go SDK's SandboxConfig.UnmarshalJSON drops them:
// Config().Volumes is empty on every read-back. See
// .operator-shared/specs/live-mount-readback.md.
type persistedConfigMounts struct {
	Mounts []persistedMount `json:"mounts"`
}

// persistedMount is one entry of that array. Only mounts are read back, so a
// single struct carries the fields of every variant it can see.
type persistedMount struct {
	Type     string `json:"type"`
	Guest    string `json:"guest"`
	Host     string `json:"host"`
	Name     string `json:"name"`
	QuotaMiB uint32 `json:"quota_mib"`
	SizeMiB  uint32 `json:"size_mib"`
	Format   string `json:"format"`
	Fstype   string `json:"fstype"`

	// StatVirtualization and HostPermissions sit next to `options` rather than
	// inside it, and only Bind, Named, and Owned carry them.
	StatVirtualization string `json:"stat_virtualization"`
	HostPermissions    string `json:"host_permissions"`

	Storage persistedOwnedStorage `json:"storage"`
	Options persistedMountFlags   `json:"options"`
}

// persistedOwnedStorage is the `storage` tag of an Owned mount, tagged by kind
// with upstream's snake_case spellings.
type persistedOwnedStorage struct {
	Kind        string `json:"kind"`
	QuotaMiB    uint32 `json:"quota_mib"`
	CapacityMiB uint32 `json:"capacity_mib"`
}

// volumeKind maps the persisted storage tag onto the SDK's volume kind. The
// runtime spells the directory kind "directory"; the SDK constant is "dir".
func (s persistedOwnedStorage) volumeKind() msbSdk.VolumeKind {
	if s.Kind == "disk" {
		return msbSdk.VolumeKindDisk
	}
	return msbSdk.VolumeKindDir
}

// persistedMountFlags is the nested `options` object every variant shares.
type persistedMountFlags struct {
	Readonly bool `json:"readonly"`
	Noexec   bool `json:"noexec"`
	Nosuid   bool `json:"nosuid"`
	Nodev    bool `json:"nodev"`
}

func (m persistedMount) mountOptions() msbSdk.MountOptions {
	return msbSdk.MountOptions{
		Readonly:           m.Options.Readonly,
		Noexec:             m.Options.Noexec,
		Nosuid:             m.Options.Nosuid,
		Nodev:              m.Options.Nodev,
		StatVirtualization: msbSdk.StatVirtualization(m.StatVirtualization),
		HostPermissions:    msbSdk.HostPermissions(m.HostPermissions),
	}
}

// errUnknownMountType marks a persisted mount variant this launcher does not
// know.
var errUnknownMountType = errors.New("unknown mount type")

// ParseConfigMounts decodes the mounts a persisted sandbox config records, keyed
// by guest path. It is the launcher's source of truth for a live VM's mounts,
// because Config() cannot supply them (specs/live-mount-readback.md).
//
// A config without a `mounts` key yields an empty map and no error: a record
// that does not describe mounts is valid and must not read as "every mount
// changed". A mount variant this launcher does not know (a newer runtime) is
// skipped, so one new kind cannot disable detection of the mounts we do manage.
func ParseConfigMounts(configJSON string) (map[string]msbSdk.MountConfig, error) {
	var persisted persistedConfigMounts
	if err := json.Unmarshal([]byte(configJSON), &persisted); err != nil {
		return nil, fmt.Errorf("parsing persisted sandbox config: %w", err)
	}

	parsed := make(map[string]msbSdk.MountConfig, len(persisted.Mounts))
	for _, entry := range persisted.Mounts {
		mount, err := entry.mountConfig()
		if errors.Is(err, errUnknownMountType) {
			continue
		}
		if err != nil {
			return nil, err
		}
		parsed[entry.Guest] = mount
	}
	return parsed, nil
}

// mountConfig maps one persisted entry onto the SDK's MountConfig. It reports
// errUnknownMountType for a variant this launcher does not know.
func (m persistedMount) mountConfig() (msbSdk.MountConfig, error) {
	if m.Guest == "" {
		return msbSdk.MountConfig{}, fmt.Errorf(
			"parsing persisted sandbox config: %s mount without a guest path",
			m.Type,
		)
	}

	flags := m.mountOptions()
	switch m.Type {
	case "Bind":
		flags.QuotaMiB = m.QuotaMiB
		return msbSdk.Mount.Bind(m.Host, flags), nil
	case "Named":
		return msbSdk.Mount.Named(m.Name, flags), nil
	case "Tmpfs":
		return msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{
			SizeMiB:  m.SizeMiB,
			Readonly: flags.Readonly,
			Noexec:   flags.Noexec,
			Nosuid:   flags.Nosuid,
			Nodev:    flags.Nodev,
		}), nil
	case "Owned":
		return msbSdk.Mount.Owned(msbSdk.OwnedVolumeOptions{
			Kind:               m.Storage.volumeKind(),
			SizeMiB:            m.Storage.CapacityMiB,
			QuotaMiB:           m.Storage.QuotaMiB,
			Readonly:           flags.Readonly,
			Noexec:             flags.Noexec,
			Nosuid:             flags.Nosuid,
			Nodev:              flags.Nodev,
			StatVirtualization: flags.StatVirtualization,
			HostPermissions:    flags.HostPermissions,
			// Guest uid/gid overrides are not compared by any consumer, so the
			// read-back leaves them unset rather than reconstructing them.
			Owner: nil,
		}), nil
	case "DiskImage":
		return msbSdk.Mount.Disk(m.Host, msbSdk.DiskOptions{
			// Upstream serializes DiskImageFormat as the bare variant name
			// ("Raw"), while DiskOptions.Format is the lowercase hint.
			Format:   strings.ToLower(m.Format),
			Fstype:   m.Fstype,
			Readonly: flags.Readonly,
			Noexec:   flags.Noexec,
			Nosuid:   flags.Nosuid,
			Nodev:    flags.Nodev,
		}), nil
	default:
		return msbSdk.MountConfig{}, fmt.Errorf("%w: %q", errUnknownMountType, m.Type)
	}
}
