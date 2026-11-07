package tty

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// fakeSink simulates a file descriptor: it accepts at most max bytes per
// write (short writes without an error), and optionally fails a numbered
// write with err.
type fakeSink struct {
	buf     bytes.Buffer
	max     int
	failOn  int
	failErr error
	calls   int
}

func (f *fakeSink) Write(p []byte) (int, error) {
	f.calls++
	n := len(p)
	if f.max > 0 && n > f.max {
		n = f.max
	}
	_, _ = f.buf.Write(p[:n])
	if f.failOn > 0 && f.calls >= f.failOn {
		return n, f.failErr
	}
	return n, nil
}

func newTestOutput(sink io.Writer, profile colorprofile.Profile, chunkSize int) *stdoutWriter {
	o := new(stdoutWriter)
	o.prof = &colorprofile.Writer{
		Forward: &chunkWriter{w: sink, o: o, chunkSize: chunkSize},
		Profile: profile,
	}
	return o
}

// referenceOutput renders s through a plain colorprofile writer, without any
// chunking of the forward writes.
func referenceOutput(s string, profile colorprofile.Profile) []byte {
	var buf bytes.Buffer
	w := colorprofile.NewWriter(&buf, nil)
	w.Profile = profile
	_, _ = w.Write([]byte(s))
	return buf.Bytes()
}

func TestShortWritesAreRetried(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("gum style line with \x1b[38;2;1;2;3mtrue color\x1b[0m text\n", 500)

	for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.NoTTY} {
		want := referenceOutput(input, profile)
		for _, chunk := range []int{1, 3, 7, 64, 4096, writeChunkSize} {
			sink := &fakeSink{max: 7}
			o := newTestOutput(sink, profile, chunk)
			if err := o.writeAll([]byte(input)); err != nil {
				t.Fatalf("profile=%v chunk=%d: unexpected error: %v", profile, chunk, err)
			}
			if !bytes.Equal(sink.buf.Bytes(), want) {
				t.Fatalf("profile=%v chunk=%d: output mismatch: got %d bytes, want %d", profile, chunk, sink.buf.Len(), len(want))
			}
		}
	}
}

func TestOutputIndependentOfChunkSize(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("abcdefghijklmnopqrstuvwxyz\x1b[31m!\x1b[0m\n", 2000)

	var first []byte
	for _, chunk := range []int{1, 5, 33, 1024, 65536} {
		sink := &fakeSink{}
		o := newTestOutput(sink, colorprofile.TrueColor, chunk)
		if err := o.writeAll([]byte(input)); err != nil {
			t.Fatalf("chunk=%d: unexpected error: %v", chunk, err)
		}
		if first == nil {
			first = sink.buf.Bytes()
			continue
		}
		if !bytes.Equal(sink.buf.Bytes(), first) {
			t.Fatalf("chunk=%d changed output: %d bytes vs %d", chunk, sink.buf.Len(), len(first))
		}
	}
}

func TestSuccessfulWritesConcatenateWithoutExtraBytes(t *testing.T) {
	t.Parallel()

	o := newTestOutput(&fakeSink{max: 5}, colorprofile.TrueColor, 4)
	if err := o.writeAll([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := o.writeAll([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	if err := o.writeAll([]byte("world\n")); err != nil {
		t.Fatal(err)
	}

	sink := o.prof.Forward.(*chunkWriter).w.(*fakeSink)
	if got, want := sink.buf.String(), "hello world\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBrokenPipeIsQuietAndSticky(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{max: 4, failOn: 3, failErr: &os.PathError{Op: "write", Path: "|1", Err: syscall.EPIPE}}
	o := newTestOutput(sink, colorprofile.TrueColor, 16)

	err := o.writeAll([]byte(strings.Repeat("x", 200)))
	if !errors.Is(err, ErrBrokenPipe) {
		t.Fatalf("got %v, want ErrBrokenPipe", err)
	}

	callsAfterFailure := sink.calls
	// Further writes must not touch the descriptor again and must report the
	// same condition without repeating an error.
	for range 3 {
		if err := o.writeAll([]byte("more output")); !errors.Is(err, ErrBrokenPipe) {
			t.Fatalf("later write got %v, want ErrBrokenPipe", err)
		}
	}
	if sink.calls != callsAfterFailure {
		t.Fatalf("sink was touched %d extra times after pipe closure", sink.calls-callsAfterFailure)
	}
}

func TestRealWriteFailureIsReportedOnceAndSticky(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{failOn: 1, failErr: &os.PathError{Op: "write", Path: "|1", Err: syscall.ENOSPC}}
	o := newTestOutput(sink, colorprofile.TrueColor, 16)

	err := o.writeAll([]byte("x"))
	if errors.Is(err, ErrBrokenPipe) {
		t.Fatal("ENOSPC must not be treated as a broken pipe")
	}
	if !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("error must contain the reason, got: %v", err)
	}

	callsAfterFailure := sink.calls
	err2 := o.writeAll([]byte("y"))
	if err2 != err {
		t.Fatalf("later write returned a different error: %v vs %v", err2, err)
	}
	if sink.calls != callsAfterFailure {
		t.Fatalf("sink was touched %d extra times after failure", sink.calls-callsAfterFailure)
	}
}

// zeroSink accepts nothing: every write reports a zero-byte short write.
type zeroSink struct{ calls int }

func (z *zeroSink) Write(p []byte) (int, error) {
	z.calls++
	return 0, nil
}

func TestZeroByteWriteIsFailure(t *testing.T) {
	t.Parallel()

	sink := &zeroSink{}
	o := newTestOutput(sink, colorprofile.TrueColor, 16)

	err := o.writeAll([]byte("data"))
	if err == nil || errors.Is(err, ErrBrokenPipe) {
		t.Fatalf("got %v, want a non-pipe failure", err)
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got %v, want io.ErrShortWrite", err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink touched %d times, want 1", sink.calls)
	}
}

func TestBrokenPipeDetection(t *testing.T) {
	t.Parallel()

	cases := []error{
		syscall.EPIPE,
		&os.PathError{Op: "write", Path: "|1", Err: syscall.EPIPE},
	}
	for _, err := range cases {
		if !isBrokenPipe(err) {
			t.Errorf("isBrokenPipe(%v) = false, want true", err)
		}
	}
	if isBrokenPipe(syscall.ENOSPC) {
		t.Error("ENOSPC must not be classified as a broken pipe")
	}
}
