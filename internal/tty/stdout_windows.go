//go:build windows

package tty

import "os"

// stdoutClosedAtStartup cannot distinguish a closed stdout on Windows;
// write-time errors still surface through the regular write path.
func stdoutClosedAtStartup(_ *os.File) error {
	return nil
}
