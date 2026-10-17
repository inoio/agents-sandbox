package runtimemigration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Filesystem seams. A real filesystem makes some failure branches (chmod, copy,
// close, readlink) impractical to trigger, so tests substitute them to exercise
// the error handling.
var (
	ioCopy    = io.Copy
	chmodPath = os.Chmod
	closeFile = (*os.File).Close
	readLink  = os.Readlink
)

// saveMigration records a migration entry on the transaction's document and
// commits it, so other registry entries and the campaign's own checkpoints
// survive. It must run inside the root transaction that owns the lock.
func saveMigration(tx *state.RootTxn, m state.Migration) error {
	tx.State().SetMigration(MigrationName, m)
	return tx.Commit()
}

// CopyVolumes runs the legacy home-volume copy campaign under the host-only root
// lock, recording progress in the migration registry so an interrupted campaign
// resumes exactly and a concurrent session cannot clobber a live copy.
//
// A terminal migration (completed or dismissed) is a no-op. Starting from
// pending records the legacy targets already present in the private home as Kept
// (dismissed from the migration at the user's choice), so accepting the offer
// neither discards nor re-copies a home the user already has. A volume-scoped
// copy failure is recorded in Excluded (with a warning) only once its partial
// destination is confirmed removed; if the destination cannot be removed the
// campaign aborts, because completing would let the home resolver adopt the
// partial. A campaign-scoped failure (the lock, the state read/write, or the home
// listing) likewise returns the error and leaves the status copying so the next
// session resumes.
//
// The whole campaign runs inside one root transaction, so the root lock is
// deliberately held across the msb copy I/O (which can take minutes): a
// non-blocking acquirer (the upgrade check or the legacy updater fold) defers to
// a later run instead of racing a live copy.
func CopyVolumes(ctx context.Context, client msb.Client, ui termio.UI) error {
	_, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		return copyVolumesLocked(ctx, client, ui, tx)
	})
	return err
}

// copyVolumesLocked runs the campaign body with the root transaction's document
// and lock held. It commits after each volume so an interruption resumes exactly.
func copyVolumesLocked(ctx context.Context, client msb.Client, ui termio.UI, tx *state.RootTxn) error {
	m, _ := tx.State().Migration(MigrationName)
	if m.Status == state.MigrationCompleted || m.Status == state.MigrationDismissed {
		return nil
	}

	legacyHome, err := runtime.LegacyHome()
	if err != nil {
		return err
	}
	if m.Status != state.MigrationCopying {
		m, err = startCampaign(tx, legacyHome)
		if err != nil {
			return err
		}
	}

	names, err := necessaryVolumesFor(m)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := copyVolume(ctx, client, legacyHome, name); err != nil {
			if removeErr := removeVolume(ctx, client, name); removeErr != nil {
				// The destination partial could not be removed, so it is still a
				// usable home under a migration target. Abort as campaign-scoped
				// rather than record an exclusion that completion would seal in.
				return fmt.Errorf("copy volume %q: %w", name, removeErr)
			}
			m.Excluded = append(m.Excluded, name)
			if serr := saveMigration(tx, m); serr != nil {
				return serr
			}
			ui.Warnf("skipping legacy home volume %q: %v", name, err)
			continue
		}
		m.Copied = append(m.Copied, name)
		if err := saveMigration(tx, m); err != nil {
			return err
		}
		ui.Infof("copied legacy home volume %q", name)
	}
	return finalizeMigration(tx, m, ui)
}

// startCampaign opens the migration campaign, recording the legacy targets
// already present in the private home as Kept and persisting the copying status,
// so an interrupted campaign resumes without re-asking for them.
func startCampaign(tx *state.RootTxn, legacyHome string) (state.Migration, error) {
	present, err := presentLegacyTargets(legacyHome, runtime.PrivateHome())
	if err != nil {
		return state.Migration{}, err
	}
	m := state.Migration{Status: state.MigrationCopying, Kept: present}
	return m, saveMigration(tx, m)
}

// finalizeMigration warns about the volumes the campaign could not copy (their
// legacy data was left behind) and records the terminal completed status. The
// transient Copied/Kept/Excluded lists are dropped by the terminal write.
func finalizeMigration(tx *state.RootTxn, m state.Migration, ui termio.UI) error {
	if len(m.Excluded) > 0 {
		ui.Warnf(
			"%d legacy home volume(s) could not be copied and were left behind: %s",
			len(m.Excluded),
			strings.Join(m.Excluded, ", "),
		)
	}
	return saveMigration(tx, state.Migration{Status: state.MigrationCompleted})
}

// copyVolume removes any leftover partial under the target name, creates the
// destination volume, and copies the legacy tree into it host-side. Completion
// is recorded by the caller in the host-owned migration registry, never as a
// file inside the guest-writable volume. A failure leaves any partial
// destination for the caller to remove before it can exclude the volume.
func copyVolume(ctx context.Context, client msb.Client, legacyHome, name string) error {
	if err := removeVolume(ctx, client, name); err != nil {
		return err
	}

	volume, err := client.CreateVolume(ctx, name, msbSdk.WithVolumeKind(msbSdk.VolumeKindDir))
	if err != nil {
		return fmt.Errorf("create volume %q: %w", name, err)
	}

	src := filepath.Join(legacyHome, "volumes", name)
	if err := copyTree(src, volume.Path()); err != nil {
		return fmt.Errorf("copy volume %q: %w", name, err)
	}
	return nil
}

// removeVolume removes the private volume under name, if present. It returns an
// error when a volume is present but cannot be removed, so a caller can refuse
// to proceed while a usable partial destination remains. An absent volume needs
// no removal and is not an error.
func removeVolume(ctx context.Context, client msb.Client, name string) error {
	if _, err := client.GetVolume(ctx, name); err != nil {
		return nil //nolint:nilerr // a failed lookup means there is nothing to remove
	}
	if err := client.RemoveVolume(ctx, name); err != nil {
		return fmt.Errorf("remove volume %q: %w", name, err)
	}
	return nil
}

// copyTree recursively copies the tree rooted at src into dst, preserving file
// mode and modification time. Directories are copied depth-first so their
// metadata is applied after their children. Symlinks are recreated as symlinks
// (never followed); non-regular entries (FIFOs, sockets, devices) are skipped.
// src must exist.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return copySymlink(src, dst)
	}
	if info.IsDir() {
		return copyDir(src, dst, info)
	}
	if !info.Mode().IsRegular() {
		// FIFOs, sockets, and device nodes cannot be meaningfully copied:
		// opening a FIFO blocks forever and a socket fails the copy. Skip them.
		return nil
	}
	return copyFile(src, dst, info)
}

func copyDir(src, dst string, info os.FileInfo) error {
	perm := info.Mode().Perm()
	// Create the destination writable regardless of the source mode, so a
	// read-only source directory can still have its children written. The
	// source mode is restored below, after the children exist.
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyTree(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}

	// Re-apply dir metadata after writing children, which would otherwise bump
	// the destination directory's mtime.
	if err := chmodPath(dst, perm); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func copyFile(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	perm := info.Mode().Perm()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if _, err := ioCopy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := closeFile(out); err != nil {
		return err
	}

	if err := chmodPath(dst, perm); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

func copySymlink(src, dst string) error {
	target, err := readLink(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, dst)
}
