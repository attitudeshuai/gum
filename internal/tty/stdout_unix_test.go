//go:build !windows

package tty

import (
	"os"
	"syscall"
	"testing"
)

func TestStdoutClosedDetection(t *testing.T) {
	t.Parallel()

	// The runtime substitutes a closed stdout with /dev/null opened
	// read-write: it must be recognized as closed.
	rwDevNull, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}
	defer rwDevNull.Close()
	if err := stdoutClosedAtStartup(rwDevNull); err == nil {
		t.Error("read-write /dev/null must be detected as closed stdout")
	}

	// An intentional write-only redirect to /dev/null is valid.
	woDevNull, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}
	defer woDevNull.Close()
	if err := stdoutClosedAtStartup(woDevNull); err != nil {
		t.Errorf("write-only /dev/null must be valid: %v", err)
	}

	// Read-only /dev/null is not the runtime substitution.
	roDevNull, err := os.OpenFile("/dev/null", os.O_RDONLY, 0)
	if err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}
	defer roDevNull.Close()
	if err := stdoutClosedAtStartup(roDevNull); err != nil {
		t.Errorf("read-only /dev/null is not flagged: %v", err)
	}

	// Regular files and pipes are valid stdout destinations.
	f, err := os.CreateTemp(t.TempDir(), "gum-stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := stdoutClosedAtStartup(f); err != nil {
		t.Errorf("regular file must be valid stdout: %v", err)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if err := stdoutClosedAtStartup(pw); err != nil {
		t.Errorf("pipe must be valid stdout: %v", err)
	}
}

func TestFcntlFlagsOnPipe(t *testing.T) {
	t.Parallel()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	flags, errno := fcntl(int(pw.Fd()), fGetFL, 0)
	if errno != 0 {
		t.Fatalf("F_GETFL failed: %v", errno)
	}
	if flags&oACCMode != syscall.O_WRONLY {
		t.Fatalf("pipe flags = %#o, want write-only", flags)
	}
}
