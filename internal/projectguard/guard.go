// Package projectguard holds, for one command, the project config files the
// user approved, and verifies that every read of a project file returns exactly
// the approved bytes (see specs/configuration/project-config-trust.md). It is
// a leaf: the readers in configread, filewalk, viperconfig, homeconfig,
// envsecret, configmerge, and image route their project reads through it.
package projectguard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ErrNotApproved reports a project file read whose bytes differ from the
// approved ones, or a file under the project config dir the approval does not
// hold.
var ErrNotApproved = errors.New("project config changed since it was approved")

// Approval is what the user approved for one path: the digest of its bytes
// (hex SHA-256), or that the source was absent and must stay unread.
type Approval struct {
	Digest string
	Absent bool
}

type guard struct {
	roots     []string
	reads     map[string]Approval
	violation error
}

var (
	mutex sync.Mutex
	// current starts as an empty approval, under which every read passes.
	current = &guard{roots: nil, reads: nil, violation: nil}
)

// Install makes reads the approval for this command: every file under roots
// must be in reads with the digest of the bytes read, and every other path in
// reads is verified too. Paths are compared absolute and cleaned, however they
// are spelled. Before any Install every read passes. It returns a function
// restoring the previous approval.
func Install(roots []string, reads map[string]Approval) func() {
	normalized := make(map[string]Approval, len(reads))
	for path, approval := range reads {
		normalized[absolute(path)] = approval
	}
	mutex.Lock()
	defer mutex.Unlock()
	previous := current
	current = &guard{roots: roots, reads: normalized, violation: nil}
	return func() {
		mutex.Lock()
		defer mutex.Unlock()
		current = previous
	}
}

// Violation returns the first read that did not match the approval, or nil.
func Violation() error {
	mutex.Lock()
	defer mutex.Unlock()
	return current.violation
}

// ReadFile reads path with read and verifies the bytes against the approval.
// Without an approval it reads through. An approved absence is not read. A path
// under a guarded root that the approval does not hold may only be missing. An
// approved file must still read, with the approved bytes. A refusal is returned
// and recorded as the command's violation.
func ReadFile(path string, read func(string) ([]byte, error)) ([]byte, error) {
	mutex.Lock()
	defer mutex.Unlock()
	absolutePath := absolute(path)
	approval, approved := current.reads[absolutePath]
	switch {
	case approval.Absent:
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	case !approved && !current.guarded(absolutePath):
		return read(path)
	case !approved:
		if _, statErr := os.Lstat(absolutePath); statErr != nil {
			return nil, statErr
		}
		return nil, current.record(
			fmt.Errorf("%w: %s is not part of the approved project config", ErrNotApproved, path),
		)
	}
	data, err := read(path)
	if err != nil {
		return nil, current.record(fmt.Errorf("%w: %s can no longer be read: %w", ErrNotApproved, path, err))
	}
	if digestOf(data) != approval.Digest {
		return nil, current.record(fmt.Errorf("%w: %s", ErrNotApproved, path))
	}
	return data, nil
}

// Accept makes data the approved bytes of path, for a write the launcher made
// itself. A path the approval does not hold stays unapproved.
func Accept(path string, data []byte) {
	mutex.Lock()
	defer mutex.Unlock()
	absolutePath := absolute(path)
	if _, approved := current.reads[absolutePath]; approved {
		current.reads[absolutePath] = Approval{Digest: digestOf(data), Absent: false}
	}
}

func (g *guard) record(err error) error {
	if g.violation == nil {
		g.violation = err
	}
	return err
}

func (g *guard) guarded(path string) bool {
	for _, root := range g.roots {
		// Both paths are absolute, so Rel cannot fail.
		rel, _ := filepath.Rel(root, path)
		if inside(rel) {
			return true
		}
	}
	return false
}

// absolute returns path made absolute. Without a readable working directory
// it is empty, which matches no approved path and lies under no root.
func absolute(path string) string {
	absolutePath, _ := filepath.Abs(path)
	return absolutePath
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ApprovalOf approves exactly data.
func ApprovalOf(data []byte) Approval {
	return Approval{Digest: digestOf(data), Absent: false}
}

// Missing reports that a reader found paths absent. An approved file among
// them that is indeed unusable now — gone, or a link to nothing — is recorded
// and returned as a refusal, so removing an approved file cannot make a reader
// silently fall back; one that still resolves was looked for elsewhere and is
// not refused. Existence follows links, as the readers' discovery does.
func Missing(paths ...string) error {
	mutex.Lock()
	defer mutex.Unlock()
	for _, path := range paths {
		absolutePath := absolute(path)
		approval, approved := current.reads[absolutePath]
		if !approved || approval.Absent {
			continue
		}
		if _, err := os.Stat(absolutePath); err != nil {
			return current.record(fmt.Errorf("%w: %s is gone", ErrNotApproved, path))
		}
	}
	return nil
}

// CheckListing reports what a reader found under root, as paths, and refuses
// an approved file under root it did not find although relevant, given the
// file's path relative to root, says the reader would have taken it.
func CheckListing(root string, listed []string, relevant func(rel string) bool) error {
	found := map[string]bool{}
	for _, path := range listed {
		found[absolute(path)] = true
	}
	absoluteRoot := absolute(root)
	mutex.Lock()
	defer mutex.Unlock()
	for _, path := range slices.Sorted(maps.Keys(current.reads)) {
		rel, _ := filepath.Rel(absoluteRoot, path)
		if current.reads[path].Absent || found[path] || !inside(rel) || !relevant(rel) {
			continue
		}
		return current.record(fmt.Errorf("%w: %s is gone", ErrNotApproved, path))
	}
	return nil
}

// inside reports whether a path relative to a root lies at or below it.
func inside(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
