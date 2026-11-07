//go:build !windows

package tty

import (
	"errors"
	"syscall"
)

// isBrokenPipe reports whether err means the reading end of the downstream
// pipe or socket was closed before the write finished.
func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
