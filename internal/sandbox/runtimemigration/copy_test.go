package runtimemigration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestCopyTreePreservesContentsAndModes(t *testing.T) {
	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "top.txt"), "top", 0o640)
	writeFileWithMode(t, filepath.Join(src, "sub", "nested.txt"), "nested", 0o600)
	if err := os.MkdirAll(filepath.Join(src, "emptydir"), 0o750); err != nil {
		t.Fatalf("create empty dir: %v", err)
	}

	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}

	assertFileWithMode(t, filepath.Join(dst, "top.txt"), "top", 0o640)
	assertFileWithMode(t, filepath.Join(dst, "sub", "nested.txt"), "nested", 0o600)

	info, err := os.Stat(filepath.Join(dst, "emptydir"))
	if err != nil {
		t.Fatalf("stat copied empty dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("emptydir is not a directory")
	}
}

func TestCopyTreePreservesModTime(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "stamped.txt")
	writeFileWithMode(t, file, "x", 0o600)
	want := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(file, want, want); err != nil {
		t.Fatalf("chtimes src: %v", err)
	}

	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "stamped.txt"))
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if !info.ModTime().Equal(want) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), want)
	}
}

func TestCopyTreePreservesSymlinks(t *testing.T) {
	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "target.txt"), "x", 0o600)
	if err := os.Symlink("target.txt", filepath.Join(src, "link")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}

	info, err := os.Lstat(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("lstat copied link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("copied link mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target.txt" {
		t.Errorf("link target = %q, want %q", target, "target.txt")
	}
}

func TestCopyTreeMissingSourceErrors(t *testing.T) {
	err := copyTree(filepath.Join(t.TempDir(), "absent"), t.TempDir())
	if err == nil {
		t.Fatal("copyTree() error = nil, want error for missing source")
	}
}

// TestCopyTreeCopiesChildrenOfReadOnlyDir pins that a source directory whose
// own mode forbids writing can still have its children copied: the destination
// must be created writable, populated, and only then chmod'd to the source mode.
func TestCopyTreeCopiesChildrenOfReadOnlyDir(t *testing.T) {
	src := t.TempDir()
	readOnly := filepath.Join(src, "readonly")
	if err := os.MkdirAll(readOnly, 0o750); err != nil {
		t.Fatalf("create readonly dir: %v", err)
	}
	writeFileWithMode(t, filepath.Join(readOnly, "child.txt"), "child", 0o600)
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatalf("chmod readonly dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o750) })

	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "readonly"), 0o750) })

	assertFileWithMode(t, filepath.Join(dst, "readonly", "child.txt"), "child", 0o600)
	info, err := os.Stat(filepath.Join(dst, "readonly"))
	if err != nil {
		t.Fatalf("stat copied readonly dir: %v", err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Errorf("copied readonly dir mode = %o, want 0o500", info.Mode().Perm())
	}
}

// TestCopyTreeSkipsSpecialFiles pins that FIFOs and other non-regular entries
// are skipped rather than opened: opening a FIFO blocks forever and a socket
// fails the whole copy. The timeout turns a regression (a hang) into a failure.
func TestCopyTreeSkipsSpecialFiles(t *testing.T) {
	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "regular.txt"), "regular", 0o600)

	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO on this platform: %v", err)
	}

	dst := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- copyTree(src, dst) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copyTree() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyTree() hung on a special file")
	}

	assertFileWithMode(t, filepath.Join(dst, "regular.txt"), "regular", 0o600)
	if _, err := os.Lstat(filepath.Join(dst, "pipe")); !os.IsNotExist(err) {
		t.Errorf("special file was copied (lstat err = %v), want it skipped", err)
	}
}

func TestCopyVolumesCopiesIntoCreatedVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	writeFileWithMode(t, filepath.Join(legacy, "volumes", name, "config", "settings.json"), "{}", 0o600)

	target := t.TempDir()
	var created []string
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, volumeName string, opts ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			created = append(created, volumeName)
			return &msb.MockVolumeHandle{Name_: volumeName, Path_: target}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes() error = %v", err)
	}

	if len(created) != 1 || created[0] != name {
		t.Fatalf("CreateVolume calls = %v, want [%q]", created, name)
	}
	assertFileWithMode(t, filepath.Join(target, "config", "settings.json"), "{}", 0o600)
}

// TestCopyVolumesAbortsWhenFailedCopyLeavesDestination pins the "no usable
// partial" invariant: a copy failure is only safe to exclude once its partial
// destination is confirmed removed. When removal fails, completing would let the
// home resolver adopt the leftover partial under the project's recorded name, so
// the campaign must abort and leave the status copying for a retry.
func TestCopyVolumesAbortsWhenFailedCopyLeavesDestination(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)

	removeErr := errors.New("remove boom")
	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, volumeName string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
		RemoveVolumeFn: func(_ context.Context, _ string) error { return removeErr },
		CreateVolumeFn: func(_ context.Context, volumeName string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: volumeName}, nil
		},
	}

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a campaign-scoped abort when the partial cannot be removed")
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCopying {
		t.Fatalf("status = %q, want copying (campaign left open for a retry)", m.Status)
	}
}

// TestCopyVolumesStartListingErrorAborts pins that a failure to derive the
// already-present targets (here, the legacy volumes directory is unlistable)
// aborts before the campaign records a copying status.
func TestCopyVolumesStartListingErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	makeLegacyVolumesUnlistable(t, installLegacyHome(t))

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want the listing failure to abort the campaign")
	}
	root, _ := state.ReadRootState()
	if m, ok := root.Migration(MigrationName); ok && m.Status == state.MigrationCopying {
		t.Fatalf("status = %q, want still pending (no campaign started)", m.Status)
	}
}

// TestCopyVolumesPreservesAlreadyPresentTarget pins that accepting the offer
// never discards a home the user already has: a legacy target already present in
// the private home is recorded as copied when the campaign starts, so it is left
// alone while the remaining targets are copied.
func TestCopyVolumesPreservesAlreadyPresentTarget(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	present := naming.HomePrefix + "present"
	missing := naming.HomePrefix + "missing"
	addVolumeDir(t, legacy, present)
	addVolumeDir(t, legacy, missing)

	privateHome := runtime.PrivateHome()
	if err := os.MkdirAll(filepath.Join(privateHome, "volumes", present), 0o750); err != nil {
		t.Fatalf("seed already-present private volume: %v", err)
	}

	target := t.TempDir()
	var created []string
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			created = append(created, n)
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	if len(created) != 1 || created[0] != missing {
		t.Fatalf("created = %v, want only %q (already-present target preserved)", created, missing)
	}
	if slices.Contains(client.RemovedVolumes, present) {
		t.Errorf("RemovedVolumes = %v, want already-present %q left alone", client.RemovedVolumes, present)
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", m.Status)
	}
}

// TestCopyVolumesRecordsProgressAndCompletes pins the terminal outcome: a clean
// campaign collapses to a completed migration and drops the transient lists.
func TestCopyVolumesRecordsProgressAndCompletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	writeFileWithMode(t, filepath.Join(legacy, "volumes", name, "f.txt"), "x", 0o600)
	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	root, _ := state.ReadRootState()
	m, _ := root.Migration(MigrationName)
	if m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", m.Status)
	}
	if len(m.Copied) != 0 || len(m.Excluded) != 0 {
		t.Fatalf("terminal migration kept transient lists: %+v", m)
	}
}

// TestCopyVolumesExcludesVolumeScopedFailureAndCompletes pins that one bad
// volume is excluded with a warning while the rest copy and the campaign still
// reaches completed. Excluded is transient (dropped at the terminal word), so
// the excluded volume is observed in the persisted state mid-campaign, when the
// next volume is being copied.
func TestCopyVolumesExcludesVolumeScopedFailureAndCompletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	bad := naming.HomePrefix + "bad"
	good := naming.HomePrefix + "good"
	addVolumeDir(t, legacy, bad)
	addVolumeDir(t, legacy, good)
	writeFileWithMode(t, filepath.Join(legacy, "volumes", good, "f.txt"), "x", 0o600)
	target := t.TempDir()
	var midCampaign state.Migration
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			if n == bad {
				return nil, errors.New("volume boom")
			}
			root, _ := state.ReadRootState()
			midCampaign, _ = root.Migration(MigrationName)
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	if midCampaign.Status != state.MigrationCopying ||
		len(midCampaign.Excluded) != 1 || midCampaign.Excluded[0] != bad {
		t.Fatalf("mid-campaign migration = %+v, want copying with %q excluded", midCampaign, bad)
	}
	root, _ := state.ReadRootState()
	m, _ := root.Migration(MigrationName)
	if m.Status != state.MigrationCompleted || len(m.Excluded) != 0 {
		t.Fatalf("migration = %+v, want completed with transient lists dropped", m)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for the excluded volume")
	}
}

// TestCopyVolumesResumesFromCopying pins exact resume: a prior campaign that
// recorded "done" and crashed re-copies only the unrecorded "partial".
func TestCopyVolumesResumesFromCopying(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	done := naming.HomePrefix + "done"
	partial := naming.HomePrefix + "partial"
	addVolumeDir(t, legacy, done)
	addVolumeDir(t, legacy, partial)
	// A prior campaign copied "done" then crashed; "partial" is present but unrecorded.
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying, Copied: []string{done}},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A present private volume not recorded as copied is a partial: it must be
	// discarded (removed) before being re-copied, while "done" is left alone.
	privateHome := runtime.PrivateHome()
	if err := os.MkdirAll(filepath.Join(privateHome, "volumes", partial), 0o750); err != nil {
		t.Fatalf("seed private partial: %v", err)
	}
	target := t.TempDir()
	var created []string
	client := &msb.MockMsbClient{
		GetVolumeFn: func(_ context.Context, n string) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: n}, nil
		},
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			created = append(created, n)
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	if len(created) != 1 || created[0] != partial {
		t.Fatalf("created = %v, want only %q (resume skips copied)", created, partial)
	}
	if !slices.Contains(client.RemovedVolumes, partial) {
		t.Errorf("RemovedVolumes = %v, want %q discarded before re-copy", client.RemovedVolumes, partial)
	}
	if slices.Contains(client.RemovedVolumes, done) {
		t.Errorf("RemovedVolumes = %v, want recorded %q left alone", client.RemovedVolumes, done)
	}
}

// TestCopyVolumesDoesNotFollowCopiedSymlink pins the symlink-safety review
// finding: a legacy volume containing a symlink named like the old marker must
// not cause a host write through it. The host file survives untouched.
func TestCopyVolumesDoesNotFollowCopiedSymlink(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	hostFile := filepath.Join(t.TempDir(), "host-secret")
	writeFileWithMode(t, hostFile, "keep", 0o600)
	// A guest could plant this exact name as a symlink to a host file.
	if err := os.Symlink(
		hostFile,
		filepath.Join(legacy, "volumes", name, ".agents-sandbox-copy-complete"),
	); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	got, err := os.ReadFile(hostFile)
	if err != nil || string(got) != "keep" {
		t.Fatalf("host file = %q, %v; want untouched", got, err)
	}
}

// TestCopyVolumesExcludesCopyTreeFailureAndCompletes pins that a volume-scoped
// failure whose destination is successfully cleaned up is excluded while the
// campaign still completes.
func TestCopyVolumesExcludesCopyTreeFailureAndCompletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	writeFileWithMode(t, filepath.Join(legacy, "volumes", name, "f.txt"), "x", 0o600)

	// A regular file at the destination path makes the tree copy fail.
	dst := filepath.Join(t.TempDir(), "not-a-dir")
	writeFileWithMode(t, dst, "x", 0o600)
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: n, Path_: dst}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err != nil {
		t.Fatalf("CopyVolumes: %v", err)
	}
	root, _ := state.ReadRootState()
	if m, _ := root.Migration(MigrationName); m.Status != state.MigrationCompleted {
		t.Fatalf("status = %q, want completed", m.Status)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("no warning for the excluded volume")
	}
}

// TestCopyVolumesLockFailureAborts pins that a failure to acquire the migration
// lock aborts the campaign. A directory occupying the root-state lock path
// makes the lock open fail.
func TestCopyVolumesLockFailureAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	installLegacyHome(t)
	blockRootLock(t)

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a lock-acquisition failure")
	}
}

func TestCopyVolumesStateReadErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	writeCorruptRootState(t)
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a state read error")
	}
}

// TestCopyVolumesTerminalIsNoOp pins that a terminal migration copies nothing.
func TestCopyVolumesTerminalIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	for _, status := range []state.MigrationStatus{state.MigrationCompleted, state.MigrationDismissed} {
		if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
			MigrationName: {Status: status},
		}}); err != nil {
			t.Fatalf("seed %q: %v", status, err)
		}
		client := &msb.MockMsbClient{}
		ui := termio.NewTestMock(t)
		if err := CopyVolumes(context.Background(), client, &ui); err != nil {
			t.Fatalf("status %q: CopyVolumes: %v", status, err)
		}
		if len(client.Volumes) != 0 || len(client.RemovedVolumes) != 0 {
			t.Errorf("status %q: CopyVolumes touched volumes", status)
		}
	}
}

// TestCopyVolumesResumeListingErrorAborts pins that resuming an open campaign
// whose legacy home has become unlistable aborts (campaign-scoped) rather than
// finalizing.
func TestCopyVolumesResumeListingErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	makeLegacyVolumesUnlistable(t, installLegacyHome(t))
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a resume listing failure to abort")
	}
}

// TestCopyVolumesCopiedSaveErrorAborts pins that a failure to persist a
// successful copy's progress (here, the state root is unwritable) aborts the
// campaign-scoped run.
func TestCopyVolumesCopiedSaveErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	name := naming.HomePrefix + "legacy"
	addVolumeDir(t, legacy, name)
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	blockRootState(t)

	target := t.TempDir()
	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, n string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return &msb.MockVolumeHandle{Name_: n, Path_: target}, nil
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a save failure to abort")
	}
}

// TestCopyVolumesExclusionSaveErrorAborts pins that a failure to persist an
// exclusion aborts the campaign-scoped run.
func TestCopyVolumesExclusionSaveErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	if err := state.WriteRootState(state.RootState{Migrations: map[string]state.Migration{
		MigrationName: {Status: state.MigrationCopying},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	blockRootState(t)

	client := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, errors.New("volume boom")
		},
	}
	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), client, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want a save failure to abort")
	}
}

// TestCopyVolumesStateWriteFailureAborts pins the campaign-scoped rule: a
// failure to persist the initial copying status returns the error and aborts the
// campaign. The state directory is made read-only, so the atomic write's temp
// file cannot be created.
func TestCopyVolumesStateWriteFailureAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := installLegacyHome(t)
	addVolumeDir(t, legacy, naming.HomePrefix+"legacy")
	blockRootState(t)

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want the state write failure to abort the campaign")
	}
}

// blockRootState makes the root state write fail while leaving it readable and
// the lock acquirable, by removing write permission from the state directory:
// the atomic write's temp file cannot be created. The lock dir and file are
// pre-created so the campaign reaches its state writes rather than failing at
// the lock; a missing state file still reads as the zero state.
func blockRootState(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	dir := configpaths.Get().UserStateDir()
	locks := filepath.Join(dir, "locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		t.Fatalf("create locks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locks, "root.lock"), nil, 0o600); err != nil {
		t.Fatalf("create root lock: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("block root state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
}

func writeFileWithMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent dir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %q: %v", path, err)
	}
}

// overrideChmod makes the chmod seam fail, exercising copy error propagation
// that a real filesystem will not surface.
func overrideChmod(t *testing.T, err error) {
	t.Helper()
	orig := chmodPath
	chmodPath = func(string, os.FileMode) error { return err }
	t.Cleanup(func() { chmodPath = orig })
}

// TestCopyVolumesLegacyHomeError covers the failure to resolve the legacy home
// (here, $HOME is unset), which aborts the copy before touching any volume.
func TestCopyVolumesLegacyHomeError(t *testing.T) {
	configpaths.WithMockConfigPathsHomeError(t, errors.New("home unavailable"))

	ui := termio.NewTestMock(t)
	if err := CopyVolumes(context.Background(), &msb.MockMsbClient{}, &ui); err == nil {
		t.Fatal("CopyVolumes() error = nil, want legacy-home resolution error")
	}
}

// TestCopyTreeDirDestinationIsFile covers copyDir failing to create the
// destination because a regular file already occupies its path.
func TestCopyTreeDirDestinationIsFile(t *testing.T) {
	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "child.txt"), "x", 0o600)

	dst := filepath.Join(t.TempDir(), "blocker")
	writeFileWithMode(t, dst, "x", 0o600)

	if err := copyTree(src, dst); err == nil {
		t.Fatal("copyTree() error = nil, want MkdirAll failure for a file destination")
	}
}

// TestCopyTreeUnreadableSourceDir covers copyDir's ReadDir failure.
func TestCopyTreeUnreadableSourceDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	src := t.TempDir()
	if err := os.Chmod(src, 0o000); err != nil {
		t.Fatalf("chmod src: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o750) })

	if err := copyTree(src, t.TempDir()); err == nil {
		t.Fatal("copyTree() error = nil, want ReadDir failure for an unreadable source dir")
	}
}

// TestCopyTreeUnreadableChild covers both copyDir's child-copy error propagation
// and copyFile's open failure for an unreadable source file.
func TestCopyTreeUnreadableChild(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block reads")
	}
	src := t.TempDir()
	child := filepath.Join(src, "secret.txt")
	writeFileWithMode(t, child, "x", 0o600)
	if err := os.Chmod(child, 0o000); err != nil {
		t.Fatalf("chmod child: %v", err)
	}

	if err := copyTree(src, t.TempDir()); err == nil {
		t.Fatal("copyTree() error = nil, want open failure for an unreadable child")
	}
}

// TestCopyTreeFileDestinationIsDir covers copyFile's OpenFile failure when the
// destination path is an existing directory.
func TestCopyTreeFileDestinationIsDir(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "plain.txt")
	writeFileWithMode(t, file, "x", 0o600)

	if err := copyTree(file, t.TempDir()); err == nil {
		t.Fatal("copyTree() error = nil, want OpenFile failure for a directory destination")
	}
}

// TestCopyTreeFileCopyError covers copyFile's io.Copy failure via the seam.
func TestCopyTreeFileCopyError(t *testing.T) {
	wantErr := errors.New("copy boom")
	orig := ioCopy
	ioCopy = func(io.Writer, io.Reader) (int64, error) { return 0, wantErr }
	t.Cleanup(func() { ioCopy = orig })

	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "file.txt"), "x", 0o600)

	if err := copyTree(src, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("copyTree() error = %v, want %v", err, wantErr)
	}
}

// TestCopyTreeFileCloseError covers copyFile's close failure via the seam. The
// replacement closes the file first so the descriptor is not leaked.
func TestCopyTreeFileCloseError(t *testing.T) {
	wantErr := errors.New("close boom")
	orig := closeFile
	closeFile = func(f *os.File) error {
		_ = f.Close()
		return wantErr
	}
	t.Cleanup(func() { closeFile = orig })

	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "file.txt"), "x", 0o600)

	if err := copyTree(src, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("copyTree() error = %v, want %v", err, wantErr)
	}
}

// TestCopyTreeFileChmodError covers copyFile's chmod failure via the seam.
func TestCopyTreeFileChmodError(t *testing.T) {
	wantErr := errors.New("chmod boom")
	overrideChmod(t, wantErr)

	src := t.TempDir()
	writeFileWithMode(t, filepath.Join(src, "file.txt"), "x", 0o600)

	if err := copyTree(src, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("copyTree() error = %v, want %v", err, wantErr)
	}
}

// TestCopyTreeDirChmodError covers copyDir's chmod failure via the seam. The
// source is empty so the chmod is the first failing step.
func TestCopyTreeDirChmodError(t *testing.T) {
	wantErr := errors.New("chmod boom")
	overrideChmod(t, wantErr)

	if err := copyTree(t.TempDir(), t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("copyTree() error = %v, want %v", err, wantErr)
	}
}

// TestCopyTreeSymlinkReadlinkError covers copySymlink's readlink failure.
func TestCopyTreeSymlinkReadlinkError(t *testing.T) {
	wantErr := errors.New("readlink boom")
	orig := readLink
	readLink = func(string) (string, error) { return "", wantErr }
	t.Cleanup(func() { readLink = orig })

	src := t.TempDir()
	if err := os.Symlink("target", filepath.Join(src, "link")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := copyTree(src, t.TempDir()); !errors.Is(err, wantErr) {
		t.Fatalf("copyTree() error = %v, want %v", err, wantErr)
	}
}

// TestCopyTreeSymlinkParentIsFile covers copySymlink's MkdirAll failure when a
// regular file occupies the destination's parent path.
func TestCopyTreeSymlinkParentIsFile(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "link")
	if err := os.Symlink("target", file); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	parent := filepath.Join(t.TempDir(), "blocker")
	writeFileWithMode(t, parent, "x", 0o600)

	if err := copyTree(file, filepath.Join(parent, "link")); err == nil {
		t.Fatal("copyTree() error = nil, want MkdirAll failure for a file parent")
	}
}

// TestCopyTreeSymlinkRemoveError covers copySymlink's failure to remove an
// existing non-empty destination directory.
func TestCopyTreeSymlinkRemoveError(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "link")
	if err := os.Symlink("target", file); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "link")
	writeFileWithMode(t, filepath.Join(dst, "occupied.txt"), "x", 0o600)

	if err := copyTree(file, dst); err == nil {
		t.Fatal("copyTree() error = nil, want Remove failure for a non-empty destination")
	}
}

func assertFileWithMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(got) != content {
		t.Errorf("%q content = %q, want %q", path, got, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%q mode = %o, want %o", path, info.Mode().Perm(), mode)
	}
}
