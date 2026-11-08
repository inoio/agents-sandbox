package msb

// MountKind discriminates between the mount flavours.
type MountKind uint8

const (
	// MountKindBind is a host bind mount.
	MountKindBind MountKind = iota + 1
	// MountKindNamed is a named persistent volume.
	MountKindNamed
	// MountKindTmpfs is an in-memory tmpfs.
	MountKindTmpfs
	// MountKindDisk is a host disk image (raw / qcow2 / ...).
	MountKindDisk
	// MountKindOwned is storage removed with its sandbox.
	MountKindOwned
)

// HostPermissions is the per-mount host-permission propagation policy. The zero
// value leaves the runtime's conservative default (Private) in place.
type HostPermissions string

const (
	HostPermissionsDefault HostPermissions = ""
	HostPermissionsPrivate HostPermissions = "private"
	HostPermissionsMirror  HostPermissions = "mirror"
)

// StatVirtualization is the per-mount stat-virtualization policy for
// virtiofs-backed mounts. The zero value leaves the runtime's default
// (Strict) in place.
type StatVirtualization string

const (
	StatVirtualizationDefault StatVirtualization = ""
	StatVirtualizationStrict  StatVirtualization = "strict"
	StatVirtualizationRelaxed StatVirtualization = "relaxed"
	StatVirtualizationOff     StatVirtualization = "off"
)

// MountOwner pins the guest owner presented for host files under a bind or
// named mount that carry no per-file stat override.
type MountOwner struct {
	UID uint32
	GID uint32
}

// MountConfig describes how a host path, named volume, tmpfs, or disk image is
// mounted into the sandbox at a guest path. Bind/named/tmpfs descriptors are
// built via the Mount factory, which enforces the mutually-exclusive kinds; a
// disk or owned descriptor is built by read-back.
//
//exhaustruct:ignore
type MountConfig struct {
	Kind               MountKind
	Bind               string
	Named              string
	Disk               string
	Owned              string
	Format             string
	Fstype             string
	QuotaMiB           uint32
	SizeMiB            uint32
	Readonly           bool
	Noexec             bool
	Nosuid             bool
	Nodev              bool
	StatVirtualization StatVirtualization
	HostPermissions    HostPermissions
	Owner              *MountOwner
}

// MountOptions tunes bind and named mount factories.
//
//exhaustruct:ignore
type MountOptions struct {
	Readonly           bool
	Noexec             bool
	Nosuid             bool
	Nodev              bool
	StatVirtualization StatVirtualization
	HostPermissions    HostPermissions
	Owner              *MountOwner
	QuotaMiB           uint32
}

// TmpfsOptions tunes the Tmpfs factory.
//
//exhaustruct:ignore
type TmpfsOptions struct {
	SizeMiB  uint32
	Readonly bool
	Noexec   bool
	Nosuid   bool
	Nodev    bool
}

// Mount is the factory namespace for the launcher mount descriptors it creates
// itself (bind, named, tmpfs). The disk and owned variants are only ever
// produced by read-back, so they have no factory — a caller that needs one
// builds the MountConfig literal. Each factory copies exactly the fields the
// SDK's matching factory copies, so the adapter's translation round-trips a
// descriptor unchanged.
var Mount mountFactory

type mountFactory struct{}

// Bind returns a MountConfig that bind-mounts a host directory into the sandbox.
func (mountFactory) Bind(hostPath string, opts MountOptions) MountConfig {
	return MountConfig{
		Kind:               MountKindBind,
		Bind:               hostPath,
		Readonly:           opts.Readonly,
		Noexec:             opts.Noexec,
		Nosuid:             opts.Nosuid,
		Nodev:              opts.Nodev,
		StatVirtualization: opts.StatVirtualization,
		HostPermissions:    opts.HostPermissions,
		Owner:              opts.Owner,
		QuotaMiB:           opts.QuotaMiB,
	}
}

// Named returns a MountConfig that mounts a named persistent volume.
func (mountFactory) Named(name string, opts MountOptions) MountConfig {
	return MountConfig{
		Kind:               MountKindNamed,
		Named:              name,
		Readonly:           opts.Readonly,
		Noexec:             opts.Noexec,
		Nosuid:             opts.Nosuid,
		Nodev:              opts.Nodev,
		StatVirtualization: opts.StatVirtualization,
		HostPermissions:    opts.HostPermissions,
		Owner:              opts.Owner,
	}
}

// Tmpfs returns a MountConfig that mounts an ephemeral in-memory filesystem.
func (mountFactory) Tmpfs(opts TmpfsOptions) MountConfig {
	return MountConfig{
		Kind:     MountKindTmpfs,
		SizeMiB:  opts.SizeMiB,
		Readonly: opts.Readonly,
		Noexec:   opts.Noexec,
		Nosuid:   opts.Nosuid,
		Nodev:    opts.Nodev,
	}
}
