//go:build darwin

package tty

import (
	"syscall"
	"unsafe"
)

// fGetPath is Darwin's F_GETPATH fcntl command.
const fGetPath = 50

// fdPath returns the filesystem path backing the given file descriptor.
func fdPath(fd int) string {
	var buf [1024]byte
	_, _, e := syscall.Syscall(
		syscall.SYS_FCNTL,
		uintptr(fd),
		uintptr(fGetPath),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if e != 0 {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return ""
}
