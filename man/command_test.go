package man

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

type testStyles struct {
	Foreground string `help:"Foreground Color" env:"FOREGROUND"`
	Background string `help:"Background Color" env:"BACKGROUND"`
	Hidden     string `help:"Hidden Color" env:"HIDDEN" hidden:"true"`
}

type testChildOptions struct {
	Arg     string     `arg:"" optional:"" help:"A positional argument."`
	Plain   string     `help:"A plain flag without an environment variable" default:"x"`
	WithEnv string     `help:"An environment flag" env:"TEST_CHILD_WITH_ENV"`
	Negated bool       `help:"A negatable flag" negatable:"" default:"true" env:"TEST_CHILD_NEG"`
	Special string     `help:"Backslash \\ and 'quote' and .dot" env:"TEST_CHILD_SPECIAL"`
	Embed   testStyles `embed:"" prefix:"style." envprefix:"TEST_CHILD_STYLE_"`
}

type testGrammar struct {
	Version   kong.VersionFlag `short:"v" help:"Print the version number"`
	HiddenCmd struct{}         `cmd:"" hidden:"" help:"A hidden command"`
	Child     testChildOptions `cmd:"" help:"The child command"`
	Other     testOtherOptions `cmd:"" help:"Another command"`
}

type testOtherOptions struct {
	Text []string `arg:"" help:"Text to process."`
}

func newTestApp(t *testing.T) *kong.Application {
	t.Helper()
	k, err := kong.New(&testGrammar{}, kong.Description("A test application."))
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	return k.Model
}

func TestEscapeText(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"backslash":            {in: `a\b`, want: `a\eb`},
		"leading dot":          {in: ".dot", want: `\&.dot`},
		"leading apostrophe":   {in: "'quote", want: `\&'quote`},
		"mid-line punctuation": {in: "a.b c'd", want: "a.b c'd"},
		"combined":             {in: ".lead 'q' \\x", want: `\&.lead 'q' \ex`},
		"multiline":            {in: "ok\n.dot\n'ap", want: "ok\n\\&.dot\n\\&'ap"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := escapeText(tt.in); got != tt.want {
				t.Errorf("escapeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestEscapeName(t *testing.T) {
	if got, want := escapeName("--cursor.foreground"), `\-\-cursor.foreground`; got != want {
		t.Errorf("escapeName = %q, want %q", got, want)
	}
}

func TestResolveManDate(t *testing.T) {
	t.Run("fixed default when unset", func(t *testing.T) {
		got, err := resolveManDate(func(string) string { return "" })
		if err != nil || got != defaultManDate {
			t.Fatalf("got (%q, %v), want %q", got, err, defaultManDate)
		}
	})

	t.Run("source date epoch in UTC", func(t *testing.T) {
		// 1700000000 = 2023-11-14 UTC.
		got, err := resolveManDate(func(string) string { return "1700000000" })
		if err != nil || got != "2023-11-14" {
			t.Fatalf("got (%q, %v), want 2023-11-14", got, err)
		}
	})

	t.Run("invalid epoch errors", func(t *testing.T) {
		if _, err := resolveManDate(func(string) string { return "soon" }); err == nil {
			t.Fatal("expected error for invalid SOURCE_DATE_EPOCH")
		}
	})
}

func TestVisibleCommandsExcludesHidden(t *testing.T) {
	app := newTestApp(t)
	commands := visibleCommands(app)
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	want := []string{"child", "other"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("visible commands = %v, want %v", names, want)
	}
}

func TestFindCommand(t *testing.T) {
	app := newTestApp(t)
	if _, err := findCommand(app, "child"); err != nil {
		t.Fatalf("find child: %v", err)
	}
	if _, err := findCommand(app, "hidden-cmd"); err == nil {
		t.Fatal("hidden command must not be resolvable")
	}
	if _, err := findCommand(app, "missing"); err == nil {
		t.Fatal("unknown command must error")
	}
}

func TestPlanPagesStable(t *testing.T) {
	app := newTestApp(t)
	first, err := planPages(app, defaultManDate, "")
	if err != nil {
		t.Fatalf("planPages: %v", err)
	}
	second, err := planPages(app, defaultManDate, "")
	if err != nil {
		t.Fatalf("planPages again: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("page count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].filename != second[i].filename {
			t.Fatalf("filename order not stable at %d: %q vs %q", i, first[i].filename, second[i].filename)
		}
		if first[i].content != second[i].content {
			t.Fatalf("content for %s not stable", first[i].filename)
		}
	}
	wantFiles := []string{"gum.1", "gum-child.1", "gum-other.1"}
	for i, want := range wantFiles {
		if first[i].filename != want {
			t.Fatalf("file %d = %q, want %q", i, first[i].filename, want)
		}
	}
}

func TestRenderPageSingleOutput(t *testing.T) {
	app := newTestApp(t)

	main, err := renderPage(app, defaultManDate, "")
	if err != nil {
		t.Fatalf("renderPage main: %v", err)
	}
	if main != renderMainPage(app, defaultManDate) {
		t.Fatal("renderPage without a command must return only the main page")
	}
	if strings.Contains(main, ".TH ") && strings.Count(main, ".TH ") != 1 {
		t.Fatal("stdout page must contain a single document")
	}

	child, err := renderPage(app, defaultManDate, "child")
	if err != nil {
		t.Fatalf("renderPage child: %v", err)
	}
	if !strings.HasPrefix(child, `.TH "GUM-CHILD"`) {
		t.Fatalf("unexpected child page heading: %q", strings.SplitN(child, "\n", 2)[0])
	}

	if _, err := renderPage(app, defaultManDate, "nope"); err == nil {
		t.Fatal("unknown command must error")
	}
}

func TestPlanSingleCommand(t *testing.T) {
	app := newTestApp(t)
	pages, err := planPages(app, defaultManDate, "child")
	if err != nil {
		t.Fatalf("planPages child: %v", err)
	}
	if len(pages) != 1 || pages[0].filename != "gum-child.1" {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	app := newTestApp(t)
	child, err := findCommand(app, "child")
	if err != nil {
		t.Fatalf("find child: %v", err)
	}
	first := renderCommandPage(child, defaultManDate)
	second := renderCommandPage(child, defaultManDate)
	if first != second {
		t.Fatal("command page rendering is not deterministic")
	}
	if renderMainPage(app, defaultManDate) != renderMainPage(app, defaultManDate) {
		t.Fatal("main page rendering is not deterministic")
	}
}

func TestCommandPageContent(t *testing.T) {
	app := newTestApp(t)
	child, err := findCommand(app, "child")
	if err != nil {
		t.Fatalf("find child: %v", err)
	}
	page := renderCommandPage(child, "2023-11-14")

	required := []string{
		`.TH "GUM-CHILD" 1 "2023-11-14" "gum" "The child command"`,
		".SH NAME",
		"gum-child - The child command",
		".SH SYNOPSIS",
		`\fBgum child\fP [\fI<arg>\fP] [\fIflags\fP]`,
		".SH DESCRIPTION",
		".SH OPTIONS",
		".SH ENVIRONMENT",
		".SH COPYRIGHT",
		`\fB\-\-[no\-]negated\fP`,
		`\fB$TEST_CHILD_NEG\fP`,
		`\fB$TEST_CHILD_SPECIAL\fP`,
		`\fB$TEST_CHILD_STYLE_BACKGROUND\fP`,
		`\fB$TEST_CHILD_STYLE_FOREGROUND\fP`,
	}
	for _, fragment := range required {
		if !strings.Contains(page, fragment) {
			t.Errorf("page missing %q\n--- page ---\n%s", fragment, page)
		}
	}

	forbidden := []string{
		"TEST_CHILD_STYLE_HIDDEN", // hidden flag excluded like in help output
		".gum-man",                // implementation details never leak
	}
	for _, fragment := range forbidden {
		if strings.Contains(page, fragment) {
			t.Errorf("page must not contain %q", fragment)
		}
	}

	// Backslashes from help text are escaped and mid-line apostrophes stay.
	if !strings.Contains(page, `Backslash \e and 'quote' and .dot`) {
		t.Errorf("special characters not escaped as expected:\n%s", page)
	}

	// Environment variables are sorted within their section: BACKGROUND
	// precedes FOREGROUND.
	envSection := page[strings.Index(page, ".SH ENVIRONMENT"):]
	bg := strings.Index(envSection, "TEST_CHILD_STYLE_BACKGROUND")
	fg := strings.Index(envSection, "TEST_CHILD_STYLE_FOREGROUND")
	if bg < 0 || fg < 0 || bg > fg {
		t.Errorf("environment variables are not sorted (bg=%d fg=%d)", bg, fg)
	}
}

func TestMainPageHasIndexAndEnvironment(t *testing.T) {
	app := newTestApp(t)
	page := renderMainPage(app, defaultManDate)
	for _, fragment := range []string{
		`.TH "GUM" 1`,
		".SH COMMANDS",
		`\fBgum\-child\fP(1)`,
		`\fBgum\-other\fP(1)`,
		".SH ENVIRONMENT",
		"No environment variables are supported by this command.",
	} {
		if !strings.Contains(page, fragment) {
			t.Errorf("main page missing %q\n--- page ---\n%s", fragment, page)
		}
	}
	if strings.Contains(page, "gum-hidden-cmd") {
		t.Error("main page must not index hidden commands")
	}
}

func TestWritePages(t *testing.T) {
	app := newTestApp(t)
	pages, err := planPages(app, defaultManDate, "")
	if err != nil {
		t.Fatalf("planPages: %v", err)
	}

	t.Run("writes all pages atomically", func(t *testing.T) {
		dir := t.TempDir()
		if err := writePages(dir, pages); err != nil {
			t.Fatalf("writePages: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != len(pages) {
			t.Fatalf("got %d files, want %d", len(entries), len(pages))
		}
		for _, page := range pages {
			data, err := os.ReadFile(filepath.Join(dir, page.filename))
			if err != nil {
				t.Fatalf("read %s: %v", page.filename, err)
			}
			if string(data) != page.content {
				t.Errorf("content mismatch for %s", page.filename)
			}
			info, err := os.Stat(filepath.Join(dir, page.filename))
			if err != nil {
				t.Fatalf("stat %s: %v", page.filename, err)
			}
			if perm := info.Mode().Perm(); perm != pageMode {
				t.Errorf("mode for %s = %o, want %o", page.filename, perm, pageMode)
			}
		}
	})

	t.Run("replaces existing pages", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, pages[0].filename)
		if err := os.WriteFile(target, []byte("stale\n"), pageMode); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := writePages(dir, pages); err != nil {
			t.Fatalf("writePages: %v", err)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(data) == "stale\n" || string(data) != pages[0].content {
			t.Fatal("existing page was not replaced")
		}
	})

	t.Run("missing directory errors with path", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does", "not", "exist")
		err := writePages(missing, pages)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("expected path-bearing error, got %v", err)
		}
	})

	t.Run("file instead of directory errors with path", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "a-file")
		if err := os.WriteFile(file, []byte("x"), pageMode); err != nil {
			t.Fatalf("seed: %v", err)
		}
		err := writePages(file, pages)
		if err == nil || !strings.Contains(err.Error(), file) {
			t.Fatalf("expected path-bearing error, got %v", err)
		}
	})

	t.Run("unwritable directory errors with path", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("permissions are not enforced for root")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		err := writePages(dir, pages)
		if err == nil || !strings.Contains(err.Error(), dir) {
			t.Fatalf("expected path-bearing error, got %v", err)
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".gum-man") {
				t.Fatalf("temporary file left behind: %s", entry.Name())
			}
		}
	})
}
