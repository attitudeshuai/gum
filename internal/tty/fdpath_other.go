//go:build !windows && !darwin && !linux

package tty

// fdPath cannot be resolved portably on this platform; closed-stdout
// detection falls back to write-time errors only.
func fdPath(_ int) string {
	return ""
}
