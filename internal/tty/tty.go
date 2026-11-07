// Package tty provides tty-aware printing.
package tty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/colorprofile"
)

// writeChunkSize bounds a single underlying write to stdout. Long results
// are forwarded as contiguous chunks; no bytes are inserted between chunks
// and the delivered output never depends on this size.
const writeChunkSize = 32 * 1024

// ErrBrokenPipe reports that the reading end of the downstream pipe was
// closed before every result byte could be written. This is an expected
// condition, for example when the output is piped to a command that stops
// reading early: callers must stay silent and exit successfully.
var ErrBrokenPipe = errors.New("broken pipe")

// stdoutWriter is the process-wide pipeline for result output. Writes are
// serialized and the first definitive failure is remembered so that later
// writes neither touch stdout again nor repeat the diagnostic, while the
// process still terminates with a failing status.
type stdoutWriter struct {
	prof   *colorprofile.Writer
	mu     sync.Mutex
	sticky error
}

var stdout = sync.OnceValue(func() *stdoutWriter {
	o := new(stdoutWriter)
	// Detect a stdout that was already closed at startup before anything
	// is written: the runtime substitutes a read-write /dev/null for it,
	// so writing would otherwise report success while discarding output.
	if err := stdoutClosedAtStartup(os.Stdout); err != nil {
		o.sticky = fmt.Errorf("gum: cannot write to stdout: %w", err)
	}
	// Detect the color profile from os.Stdout itself: wrapping the sink
	// must not change terminal detection or color downgrade rules.
	o.prof = &colorprofile.Writer{
		Forward: &chunkWriter{w: os.Stdout, o: o},
		Profile: colorprofile.Detect(os.Stdout, os.Environ()),
	}
	return o
})

// Writer returns a writer to stdout that downgrades or strips ANSI sequences
// according to the environment (NO_COLOR, CLICOLOR, CLICOLOR_FORCE) and the
// capabilities of the terminal.
func Writer() io.Writer {
	return stdout().prof
}

// WriteString writes s to stdout. Short writes are retried until every byte
// is delivered or a definitive failure is recorded; the returned error
// describes the reason. A downstream closing the pipe early is reported as
// ErrBrokenPipe.
func WriteString(s string) error {
	return stdout().writeAll([]byte(s))
}

// Println writes s to stdout followed by a single newline, stripping ANSI
// sequences if stdout is not a tty.
func Println(s string) error {
	return stdout().writeAll([]byte(s + "\n"))
}

// writeAll delivers the whole of p through the color profile writer. All
// buffering is exhausted before it returns and any undelivered remainder is
// reported as an error.
func (o *stdoutWriter) writeAll(p []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.sticky != nil {
		return o.sticky
	}

	n, err := o.prof.Write(p)
	switch {
	case err != nil:
		o.fail(err)
	case n < len(p):
		// The sink below delivers every byte or reports an error, so a
		// short count without an error must not pass unnoticed.
		o.fail(io.ErrShortWrite)
	}

	return o.sticky
}

// fail records the first definitive write failure. Pipe closure is treated
// as an expected, quiet condition; every other failure wraps the reason for
// the diagnostic stream. The caller must hold o.mu.
func (o *stdoutWriter) fail(err error) {
	if o.sticky != nil {
		return
	}
	if isBrokenPipe(err) {
		o.sticky = ErrBrokenPipe
		return
	}
	o.sticky = fmt.Errorf("gum: cannot write to stdout: %w", err)
}

// chunkWriter forwards bytes in bounded chunks and retries short writes.
// Every call happens while the owning stdoutWriter lock is held, so the
// sticky failure is read and recorded without locking of its own.
type chunkWriter struct {
	w         io.Writer
	o         *stdoutWriter
	chunkSize int // 0 means writeChunkSize; overridden in tests
}

// Write implements io.Writer.
func (c *chunkWriter) Write(p []byte) (int, error) {
	if c.o.sticky != nil {
		return 0, c.o.sticky
	}

	max := c.chunkSize
	if max <= 0 {
		max = writeChunkSize
	}

	total := 0
	for len(p) > 0 {
		b := p
		if len(b) > max {
			b = b[:max]
		}
		n, err := c.w.Write(b)
		if n > 0 {
			total += n
			p = p[n:]
		}
		if err != nil {
			c.o.fail(err)
			return total, c.o.sticky
		}
		if n == 0 {
			c.o.fail(io.ErrShortWrite)
			return total, c.o.sticky
		}
	}

	return total, nil
}
