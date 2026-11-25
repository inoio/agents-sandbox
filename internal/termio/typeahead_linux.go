package termio

import "golang.org/x/sys/unix"

// tcflushInput is TCIFLUSH from asm-generic/termbits.h: the TCFLSH queue that
// holds received input not yet read.
const tcflushInput = 0

// discardTerminalInputQueue drops the input a terminal received but no reader
// consumed yet.
func discardTerminalInputQueue(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, tcflushInput)
}
