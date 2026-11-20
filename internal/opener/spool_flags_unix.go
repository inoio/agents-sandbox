//go:build !windows

package opener

import (
	"os"
	"syscall"
)

// spoolDirectoryFlags opens the spool directory as a directory without blocking
// on a guest-planted FIFO (there is no O_CREATE, so the permission argument is
// ignored).
const spoolDirectoryFlags = os.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NONBLOCK

// claimedFileFlags opens a claimed request without following a guest-planted
// symlink and without blocking on a FIFO planted as the claim.
const claimedFileFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
