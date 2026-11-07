// Package main is Gum: a tool for glamorous shell scripts.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"charm.land/gum/v2/internal/exit"
	"charm.land/gum/v2/internal/tty"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
)

const shaLen = 7

const defaultBool = "false"

var (
	// Version contains the application version number. It's set via ldflags
	// when building.
	Version = ""

	// CommitSHA contains the SHA of the commit that this application was built
	// against. It's set via ldflags when building.
	CommitSHA = ""
)

var bubbleGumPink = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

func init() {
	// By default a SIGPIPE caused by writing to stdout (or stderr) kills
	// the whole process, so an early-closing downstream such as
	// `gum style ... | head -1` would terminate gum with SIGPIPE instead
	// of an observable write result. Asking for the notification makes the
	// failing write return syscall.EPIPE, where it can be handled like any
	// other write failure; unlike signal.Ignore, child processes (for
	// example those spawned by `gum spin`) still receive the default
	// disposition at exec time.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
}

func main() {
	if Version == "" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Sum != "" {
			Version = info.Main.Version
		} else {
			Version = "unknown (built from source)"
		}
	}
	version := fmt.Sprintf("gum version %s", Version)
	if len(CommitSHA) >= shaLen {
		version += " (" + CommitSHA[:shaLen] + ")"
	}

	gum := &Gum{}
	ctx := kong.Parse(
		gum,
		kong.Description(fmt.Sprintf("A tool for %s shell scripts.", bubbleGumPink.Render("glamorous"))),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact:             true,
			Summary:             false,
			NoExpandSubcommands: true,
		}),
		kong.Vars{
			"version":                 version,
			"versionNumber":           Version,
			"defaultHeight":           "0",
			"defaultWidth":            "0",
			"defaultAlign":            "left",
			"defaultBorder":           "none",
			"defaultBorderForeground": "",
			"defaultBorderBackground": "",
			"defaultBackground":       "",
			"defaultForeground":       "",
			"defaultMargin":           "0 0",
			"defaultPadding":          "0 0",
			"defaultUnderline":        defaultBool,
			"defaultBold":             defaultBool,
			"defaultFaint":            defaultBool,
			"defaultItalic":           defaultBool,
			"defaultStrikethrough":    defaultBool,
		},
	)
	if err := ctx.Run(); err != nil {
		// The downstream closed the pipe before the result was fully read.
		// Like standard Unix filters, stop quietly and report success; the
		// result stream stays untouched and nothing is printed.
		if errors.Is(err, tty.ErrBrokenPipe) {
			return
		}
		var ex exit.ErrExit
		if errors.As(err, &ex) {
			os.Exit(int(ex))
		}
		if errors.Is(err, tea.ErrInterrupted) {
			os.Exit(exit.StatusAborted)
		}
		if errors.Is(err, tea.ErrProgramKilled) {
			fmt.Fprintln(os.Stderr, "timed out")
			os.Exit(exit.StatusTimeout)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
