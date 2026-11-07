//go:build !windows

package tty

import (
	"errors"
	"os"
	"syscall"
)

// fcntl commands and masks are stable across the Unix variants gum targets.
const (
	fGetFL      = 0x03
	devNullPath = "/dev/null"
	oACCMode    = syscall.O_ACCMODE
	oRDWR       = syscall.O_RDWR
)

// fcntl invokes the fcntl(2) system call.
func fcntl(fd, cmd, arg int) (int, syscall.Errno) {
	r0, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg))
	return int(r0), e
}

// errStdoutClosed is the reason reported when standard output was closed
// before the process started.
var errStdoutClosed = errors.New("standard output is closed")

// stdoutClosedAtStartup reports whether stdout was closed when the process
// started.
//
// When standard descriptors are closed at startup the Go runtime opens
// /dev/null read-write on the freed descriptor, so a plain write succeeds
// and silently discards the result. An intentional `>/dev/null` is opened
// write-only, which lets us tell the two situations apart.
func stdoutClosedAtStartup(f *os.File) error {
	fd := int(f.Fd())

	flags, errno := fcntl(fd, fGetFL, 0)
	if errno != 0 {
		if errors.Is(errno, syscall.EBADF) {
			return errStdoutClosed
		}
		return nil
	}
	if flags&oACCMode != oRDWR {
		return nil
	}
	if fdPath(fd) == devNullPath {
		return errStdoutClosed
	}
	return nil
}
