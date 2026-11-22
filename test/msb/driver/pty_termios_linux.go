//go:build linux

package driver

import "golang.org/x/sys/unix"

// ioctlReadTermios is the ioctl that reads a terminal's termios on Linux.
const ioctlReadTermios = unix.TCGETS
