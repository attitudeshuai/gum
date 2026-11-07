//go:build windows

package tty

import (
	"errors"
	"syscall"
)

// errnoBrokenPipe is syscall.ERROR_BROKEN_PIPE, returned by writes to a pipe
// whose other end has been closed.
const errnoBrokenPipe syscall.Errno = 109

// isBrokenPipe reports whether err means the reading end of the downstream
// pipe or socket was closed before the write finished.
func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, errnoBrokenPipe)
}
