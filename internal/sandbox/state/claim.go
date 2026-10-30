package state

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/hostlock"
)

// claimOpened is a test seam invoked right after a claim file is opened and
// before it is locked and verified. It lets a test force the open/lock race
// deterministically; production leaves it nil.
var claimOpened func(f *os.File, path string)

// AcquireClaim atomically claims a named token under the slug's state dir,
// returning claimed=true and a release func only when this caller won the
// claim. If another live holder owns it, claimed is false and release is nil.
// A dead holder's flock auto-releases, so a later caller can always retake an
// abandoned claim.
//
// Release removes the claim file (while holding the flock), so tokens a caller
// creates do not accumulate. A caller that opened a file another holder removed
// underneath it detects that via sameFile and retries on a fresh file, so the
// removal cannot hand two callers the same token.
func AcquireClaim(slug, key string) (func(), bool, error) {
	dir := filepath.Join(slugDir(slug), "claims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create claim dir: %w", err)
	}
	path := filepath.Join(dir, key+".claim")
	for range 5 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, false, fmt.Errorf("open claim %s: %w", path, err)
		}
		if claimOpened != nil {
			claimOpened(f, path)
		}
		if err := hostlock.ExclusiveNonBlocking(f); err != nil {
			_ = f.Close()
			if hostlock.IsContention(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("flock claim %s: %w", path, err)
		}
		if !sameFile(f, path) {
			// The token was released (and its file removed) between our open
			// and our lock; it is not ours, so retry on the current file.
			_ = f.Close()
			continue
		}
		return func() {
			// Remove while holding the flock, then release it.
			_ = os.Remove(path)
			_ = f.Close()
		}, true, nil
	}
	return nil, false, nil
}

// sameFile reports whether path still resolves to the open file f, guarding
// against a claim file another holder removed after this caller opened it.
func sameFile(f *os.File, path string) bool {
	onDisk, err := os.Stat(path)
	if err != nil {
		return false
	}
	open, err := f.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(onDisk, open)
}
