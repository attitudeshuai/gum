// Package man implements the gum man command.
package man

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

// Man is a gum sub-command that generates man pages.
//
// Without arguments it prints the main overview page to stdout, preserving
// the historical single-page usage. A sub-command name prints that command's
// page instead. With OutputDir the main page and one page per sub-command are
// installed into the directory.
type Man struct {
	Command   string `arg:"" optional:"" help:"Generate the man page for this command instead of the main page"`
	OutputDir string `short:"o" help:"Write the man page(s) to this directory instead of stdout"`
}

// AfterApply implements Kong AfterApply hook. It runs after the command and
// flag values have been applied to the struct.
func (m Man) AfterApply(ctx *kong.Context) error {
	// Set the man page description without color escape sequences.
	ctx.Model.Help = "A tool for glamorous shell scripts."

	date, err := resolveManDate(os.Getenv)
	if err != nil {
		return m.fail(ctx, err)
	}

	if m.OutputDir == "" {
		// Keep the historical single-page behavior: one page on stdout, the
		// main overview page unless a specific command was requested.
		content, err := renderPage(ctx.Model, date, m.Command)
		if err != nil {
			return m.fail(ctx, err)
		}
		if _, err := fmt.Fprint(ctx.Stdout, content); err != nil {
			return m.fail(ctx, err)
		}
		ctx.Exit(0)
		return nil
	}

	pages, err := planPages(ctx.Model, date, m.Command)
	if err != nil {
		return m.fail(ctx, err)
	}
	if err := writePages(m.OutputDir, pages); err != nil {
		return m.fail(ctx, err)
	}
	ctx.Exit(0)
	return nil
}

// fail reports an error on stderr and terminates with a non-zero exit code.
func (m Man) fail(ctx *kong.Context, err error) error {
	_, _ = fmt.Fprintf(ctx.Stderr, "%s man: %s\n", programName, err)
	ctx.Exit(1)
	return nil
}
