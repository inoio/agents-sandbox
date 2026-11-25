package termio

import "golang.org/x/sys/unix"

// readQueue is FREAD from sys/fcntl.h: the TIOCFLUSH queue that holds received
// input not yet read.
const readQueue = 0x0001

// discardTerminalInputQueue drops the input a terminal received but no reader
// consumed yet.
func discardTerminalInputQueue(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, readQueue)
}
