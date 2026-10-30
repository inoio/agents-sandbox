package opener

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// SpoolRelativePath is the spool directory relative to the home volume root.
// Inside the VM it is /home/dev/.local/state/agents-sandbox/open; the in-VM
// shim hardcodes the same path.
const SpoolRelativePath = ".local/state/agents-sandbox/open"

// File suffixes of the spool protocol. The shim writes <id>.request atomically
// (temp file plus rename). A watcher claims it by renaming it to <id>.claimed,
// which is atomic, so with several attached launchers exactly one handles it.
// The watcher then writes <id>.response, which the shim reads and removes.
const (
	requestSuffix  = ".request"
	claimedSuffix  = ".claimed"
	responseSuffix = ".response"
)

// requestIdentifierPattern restricts request ids to what the shim generates, so
// a guest-chosen name can never form a path.
var requestIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// spool is the host side of the spool directory. All access goes through an
// os.Root on the home volume, so a guest-planted symlink cannot point the
// watcher at host files outside the volume.
type spool struct {
	root *os.Root
}

func openSpool(homeVolumePath string) (*spool, error) {
	root, err := os.OpenRoot(homeVolumePath)
	if err != nil {
		return nil, fmt.Errorf("open home volume %s: %w", homeVolumePath, err)
	}
	return &spool{root: root}, nil
}

func (s *spool) close() { _ = s.root.Close() }

func spoolPath(name string) string { return path.Join(SpoolRelativePath, name) }

// spoolDirectoryFlags opens the spool directory as a directory without blocking
// on a guest-planted FIFO (there is no O_CREATE, so the permission argument is
// ignored).
const spoolDirectoryFlags = os.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NONBLOCK

// claimedFileFlags opens a claimed request without following a guest-planted
// symlink and without blocking on a FIFO planted as the claim.
const claimedFileFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// readDirectoryEntries is a seam over the directory read so the ReadDir error
// branch is testable: a directory opened with O_DIRECTORY cannot otherwise fail
// to read in a test.
var readDirectoryEntries = func(directory *os.File) ([]os.DirEntry, error) {
	// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="ReadDir treats every n <= 0 identically, so -1, 0 and -2 all return every entry"
	return directory.ReadDir(-1)
}

// spoolEntry is one protocol file in the spool directory.
type spoolEntry struct {
	identifier string
	suffix     string
	modified   time.Time
}

// entries lists the protocol files in the spool directory. The directory is
// missing until the shim first runs; callers treat every error as "nothing to
// do yet". The directory is opened with O_DIRECTORY|O_NONBLOCK: the path is
// guest-writable, so a guest could replace the directory with a FIFO, and a
// blocking open would then wait forever for a writer and hang the watcher (and
// session shutdown, which waits for it).
func (s *spool) entries() ([]spoolEntry, error) {
	directory, err := s.root.OpenFile(
		SpoolRelativePath,
		spoolDirectoryFlags,
		// gomutants:disable-next-line INTEGER_INCREMENT reason="the permission is ignored without O_CREATE"
		0,
	)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	directoryEntries, err := readDirectoryEntries(directory)
	if err != nil {
		return nil, err
	}
	var result []spoolEntry
	for _, directoryEntry := range directoryEntries {
		entry, ok := parseSpoolName(directoryEntry.Name())
		if !ok {
			continue
		}
		if info, infoErr := directoryEntry.Info(); infoErr == nil {
			entry.modified = info.ModTime()
		}
		result = append(result, entry)
	}
	return result, nil
}

func parseSpoolName(name string) (spoolEntry, bool) {
	for _, suffix := range []string{requestSuffix, claimedSuffix, responseSuffix} {
		identifier, found := strings.CutSuffix(name, suffix)
		if found && requestIdentifierPattern.MatchString(identifier) {
			return spoolEntry{identifier: identifier, suffix: suffix, modified: time.Time{}}, true
		}
	}
	return spoolEntry{}, false
}

// claim atomically takes ownership of a pending request. It reports false when
// another watcher (or the shim giving up) got there first.
func (s *spool) claim(identifier string) bool {
	return s.root.Rename(spoolPath(identifier+requestSuffix), spoolPath(identifier+claimedSuffix)) == nil
}

// readClaimed returns the target of a claimed request. It refuses anything but
// a regular file (a FIFO would block the watcher) and reads at most
// MaxTargetBytes+1 bytes so ClassifyTarget can reject oversized targets.
func (s *spool) readClaimed(identifier string) (string, error) {
	file, err := s.root.OpenFile(
		spoolPath(identifier+claimedSuffix),
		claimedFileFlags,
		// gomutants:disable-next-line INTEGER_INCREMENT reason="the permission is ignored without O_CREATE"
		0,
	)
	if err != nil {
		return "", err
	}
	defer file.Close()
	// A failed Stat on an open fd is folded into "not a regular file": it cannot
	// be provoked in a test, and a separate branch would stay uncovered.
	// gomutants:disable-next-line EXPRESSION_REMOVE reason="Stat on a successfully opened fd cannot fail in a test, so the statErr operand is unobservable"
	if info, statErr := file.Stat(); statErr != nil || !info.Mode().IsRegular() {
		return "", errors.New("request is not a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, MaxTargetBytes+1))
	// gomutants:disable-next-line RETURN_ERROR_NIL reason="ReadAll on a regular fd cannot fail in a test, so the error is unobservable"
	return strings.TrimSuffix(string(content), "\n"), err
}

// respond publishes the verdict for a claimed request and drops the claim. The
// response is written to a fresh exclusive temp file and renamed into place,
// so a guest-planted <id>.response symlink is replaced, never written through.
// A response that cannot be written is dropped: the shim then times out and
// tells its caller that the host did not answer.
func (s *spool) respond(identifier, response string) {
	defer func() { _ = s.root.Remove(spoolPath(identifier + claimedSuffix)) }()
	// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="the random suffix only needs enough bytes to avoid collision; its length is unobservable"
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	temporaryName := spoolPath("." + identifier + "." + hex.EncodeToString(suffix) + ".tmp")
	file, err := s.root.OpenFile(
		temporaryName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="a umask-adjusted temp file mode, not a contract; the guest only needs to read the renamed response"
		0o644,
	)
	// gomutants:disable-next-line BRANCH_IF reason="removing the early return only leaves the nil file to fail WriteString and Remove itself, so the outcome is identical"
	if err != nil {
		return
	}
	_, writeErr := file.WriteString(response + "\n")
	closeErr := file.Close()
	// gomutants:disable-next-line EXPRESSION_REMOVE reason="a write or close on a freshly created regular file cannot fail in a test, so the Join check is unobservable"
	if errors.Join(writeErr, closeErr) != nil ||
		s.root.Rename(temporaryName, spoolPath(identifier+responseSuffix)) != nil {
		_ = s.root.Remove(temporaryName)
	}
}

func (s *spool) remove(entry spoolEntry) {
	_ = s.root.Remove(spoolPath(entry.identifier + entry.suffix))
}
