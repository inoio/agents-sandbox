//go:build windows

package opener

import "os"

// Windows has no O_DIRECTORY or O_NOFOLLOW. The spike opens the spool paths
// with the default read-only flags; the opener channel's Unix FIFO/symlink
// hardening is not yet ported.
const spoolDirectoryFlags = os.O_RDONLY

const claimedFileFlags = os.O_RDONLY
