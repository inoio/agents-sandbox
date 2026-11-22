//go:build darwin

package driver

import "golang.org/x/sys/unix"

// ioctlReadTermios is the ioctl that reads a terminal's termios on Darwin.
const ioctlReadTermios = unix.TIOCGETA
