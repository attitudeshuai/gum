//go:build linux

package tty

import (
	"strconv"
	"syscall"
)

// fdPath returns the filesystem path backing the given file descriptor.
func fdPath(fd int) string {
	var buf [1024]byte
	n, err := syscall.Readlink("/proc/self/fd/"+strconv.Itoa(fd), buf[:])
	if err != nil {
		return ""
	}
	return string(buf[:n])
}
