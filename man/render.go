package man

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
)

const (
	manSection = 1

	// programName is the stable name used in page titles, synopses and file
	// names, independently of the binary's invocation name.
	programName = "gum"

	// manDateLayout is the date format used in the .TH heading.
	manDateLayout = "2006-01-02"

	// defaultManDate is the fixed date used when no reproducible date source
	// is provided, so repeated builds always produce the same output.
	defaultManDate = "1970-01-01"

	// sourceDateEpochEnv is the standard reproducible-builds variable holding
	// a Unix timestamp in seconds (see https://reproducible-builds.org/).
	sourceDateEpochEnv = "SOURCE_DATE_EPOCH"

	copyrightText = "(c) 2022-2024 Charmbracelet, Inc.\n" +
		"Released under MIT license."
)

// resolveManDate derives the .TH heading date from a reproducible source.
//
// When SOURCE_DATE_EPOCH is set it is interpreted as a Unix timestamp in
// seconds and formatted as a UTC date. Otherwise the fixed default date is
// used, guaranteeing that rebuilding on another day yields the same page.
func resolveManDate(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv(sourceDateEpochEnv))
	if raw == "" {
		return defaultManDate, nil
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid %s %q: %w", sourceDateEpochEnv, raw, err)
	}
	return time.Unix(epoch, 0).UTC().Format(manDateLayout), nil
}

// roffWriter emits roff/man source without ever interpreting user-provided
// text as macros.
type roffWriter struct {
	b      strings.Builder
	opened bool
}

// macro writes a control line (".SH", ".TP", ...). Arguments must already be
// escaped.
func (w *roffWriter) macro(line string) {
	if w.opened {
		w.b.WriteByte('\n')
	}
	w.b.WriteString(line)
	w.opened = true
}

// rich writes an already escaped line of text, possibly containing font
// escapes.
func (w *roffWriter) rich(text string) {
	if w.opened {
		w.b.WriteByte('\n')
	}
	w.b.WriteString(text)
	w.opened = true
}

// plain writes untrusted text, escaping it according to the roff rules.
func (w *roffWriter) plain(text string) {
	w.rich(escapeText(text))
}

func (w *roffWriter) heading(title string, section uint, date, source, description string) {
	w.macro(fmt.Sprintf(".TH %s %d %s %s %s",
		quoteArg(strings.ToUpper(title)),
		section,
		quoteArg(date),
		quoteArg(source),
		quoteArg(description),
	))
}

func (w *roffWriter) section(title string) {
	w.macro(".SH " + strings.ToUpper(title))
}

func (w *roffWriter) taggedParagraph() {
	w.macro(".TP")
}

func (w *roffWriter) bold(text string) {
	w.rich(`\fB` + text + `\fP`)
}

// String returns the finished document, always terminated by a newline.
func (w *roffWriter) String() string {
	return w.b.String() + "\n"
}

// quoteArg quotes a .TH macro argument.
func quoteArg(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// escapeText escapes arbitrary body text. Backslashes are replaced with \e.
// A line starting with '.' or "'" would be parsed as a roff control line, so
// such lines are prefixed with the zero-width \& escape.
func escapeText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = escapeLine(line)
	}
	return strings.Join(lines, "\n")
}

func escapeLine(line string) string {
	escaped := strings.ReplaceAll(line, `\`, `\e`)
	if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
		escaped = `\&` + escaped
	}
	return escaped
}

// escapeName escapes a command or option name. Hyphens are emitted as \- so
// the name is never used as a line-break point, and the surrounding font
// escapes ensure it can never be interpreted as a macro.
func escapeName(name string) string {
	name = strings.ReplaceAll(name, `\`, `\e`)
	return strings.ReplaceAll(name, "-", `\-`)
}

// envVar is one entry of the ENVIRONMENT section.
type envVar struct {
	name    string
	purpose string
}

// visibleCommands returns the commands documented on the main page. The
// policy matches the help output: hidden commands are omitted.
func visibleCommands(app *kong.Application) []*kong.Node {
	commands := make([]*kong.Node, 0, len(app.Children))
	for _, child := range app.Children {
		if child.Hidden || child.Type != kong.CommandNode {
			continue
		}
		commands = append(commands, child)
	}
	return commands
}

// findCommand resolves a command name (or alias) to a visible command node.
func findCommand(app *kong.Application, name string) (*kong.Node, error) {
	for _, child := range visibleCommands(app) {
		if child.Name == name {
			return child, nil
		}
		for _, alias := range child.Aliases {
			if alias == name {
				return child, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown command %q; run `%s man` to list the available commands", name, programName)
}

// documentedFlags flattens the flags visible for a node, in the same order and
// with the same visibility rules as the help output.
func documentedFlags(node *kong.Node) []*kong.Flag {
	var flags []*kong.Flag
	for _, group := range node.AllFlags(true) {
		flags = append(flags, group...)
	}
	return flags
}

// collectEnvVars returns the environment variables supported by a command,
// de-duplicated and sorted by name. Variable names come from the same kong
// metadata as the help output.
func collectEnvVars(node *kong.Node) []envVar {
	byName := make(map[string]envVar)
	for _, flag := range documentedFlags(node) {
		purpose := strings.TrimSpace(flag.Help)
		for _, env := range flag.Envs {
			if _, exists := byName[env]; exists {
				continue
			}
			byName[env] = envVar{name: env, purpose: purpose}
		}
	}
	vars := make([]envVar, 0, len(byName))
	for _, variable := range byName {
		vars = append(vars, variable)
	}
	sort.Slice(vars, func(i, j int) bool {
		return vars[i].name < vars[j].name
	})
	return vars
}

// renderPage renders a single page: the main overview page when command is
// empty, otherwise the page of that command.
func renderPage(app *kong.Application, date, command string) (string, error) {
	if command == "" {
		return renderMainPage(app, date), nil
	}
	node, err := findCommand(app, command)
	if err != nil {
		return "", err
	}
	return renderCommandPage(node, date), nil
}

// renderMainPage renders the overview page containing the command index.
func renderMainPage(app *kong.Application, date string) string {
	w := &roffWriter{}
	w.heading(programName, manSection, date, programName, app.Help)

	w.section("Name")
	w.plain(programName + " - " + app.Help)

	w.section("Synopsis")
	w.rich(`\fB` + escapeName(programName) + `\fP [\fIflags\fP] \fI<command>\fP`)

	w.section("Description")
	w.plain(app.Help)

	commands := visibleCommands(app)
	w.section("Commands")
	for _, command := range commands {
		w.taggedParagraph()
		w.rich(`\fB` + escapeName(pageName(command)) + `\fP(1)`)
		w.plain(command.Help)
	}

	w.section("Options")
	writeOptions(w, app.Node)

	writeEnvironment(w, app.Node)

	w.section("Copyright")
	w.plain(copyrightText)
	return w.String()
}

// renderCommandPage renders the page of a single sub-command.
func renderCommandPage(node *kong.Node, date string) string {
	w := &roffWriter{}
	pageTitle := pageName(node)
	w.heading(pageTitle, manSection, date, programName, node.Help)

	w.section("Name")
	w.plain(pageTitle + " - " + node.Help)

	w.section("Synopsis")
	w.rich(`\fB` + escapeName(programName+" "+node.Path()) + `\fP` + synopsisSuffix(node))

	description := node.Detail
	if strings.TrimSpace(description) == "" {
		description = node.Help
	}
	w.section("Description")
	w.plain(description)

	w.section("Options")
	writeOptions(w, node)

	writeEnvironment(w, node)

	w.section("Copyright")
	w.plain(copyrightText)
	return w.String()
}

// synopsisSuffix builds the positional-arguments and "[flags]" parts of a
// synopsis, mirroring the shape used by the built-in help output.
func synopsisSuffix(node *kong.Node) string {
	var b strings.Builder
	for _, arg := range node.Positional {
		b.WriteByte(' ')
		name := "<" + arg.Name + ">"
		if arg.IsSlice() {
			name += " ..."
		}
		token := `\fI` + escapeName(name) + `\fP`
		if !arg.Required {
			token = "[" + token + "]"
		}
		b.WriteString(token)
	}
	if len(documentedFlags(node)) > 0 {
		b.WriteString(` [\fIflags\fP]`)
	}
	return b.String()
}

// writeOptions writes the OPTIONS section entries from the kong flag metadata,
// which is the same metadata used by the built-in help output. Hidden flags
// are excluded by documentedFlags.
func writeOptions(w *roffWriter, node *kong.Node) {
	for _, flag := range documentedFlags(node) {
		w.taggedParagraph()
		w.rich(flagTitle(flag))
		w.plain(kong.DefaultHelpValueFormatter(flag.Value))
	}
}

// flagTitle renders the bold option token, matching the help output naming
// (short flag, --[no-] negation and value placeholder).
func flagTitle(flag *kong.Flag) string {
	var b strings.Builder
	if flag.Short != 0 {
		b.WriteString(`\fB` + escapeName("-"+string(flag.Short)) + `\fP, `)
	}
	if flag.IsBool() && flag.Tag.Negatable != "" {
		b.WriteString(`\fB` + escapeName("--[no-]"+flag.Name) + `\fP`)
	} else {
		b.WriteString(`\fB` + escapeName("--"+flag.Name) + `\fP`)
	}
	if !flag.IsBool() && !flag.IsCounter() {
		b.WriteString(`=\fB` + escapeName(flag.FormatPlaceHolder()) + `\fP`)
	}
	return b.String()
}

// writeEnvironment writes the ENVIRONMENT section. Variables are sorted by
// name; commands without variables get an explicit, stable notice.
func writeEnvironment(w *roffWriter, node *kong.Node) {
	w.section("Environment")
	vars := collectEnvVars(node)
	if len(vars) == 0 {
		w.plain("No environment variables are supported by this command.")
		return
	}
	for _, variable := range vars {
		w.taggedParagraph()
		w.bold(escapeText("$" + variable.name))
		if variable.purpose != "" {
			w.plain(variable.purpose)
		}
	}
}

// pageName returns the manual page name (and file stem) for a command, e.g.
// "gum-choose". Nested commands join their path with dashes.
func pageName(node *kong.Node) string {
	return programName + "-" + strings.ReplaceAll(node.Path(), " ", "-")
}
