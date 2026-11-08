// Package write provides a shell script interface for the text area bubble.
// https://github.com/charmbracelet/bubbles/tree/master/textarea
//
// It can be used to ask the user to write some long form of text (multi-line)
// input. The text the user entered will be sent to stdout.
// Text entry is completed with CTRL+D and aborted with CTRL+C or Escape.
//
// $ gum write > output.text
package write

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sync"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/editor"
)

// tempFilePattern is the pattern used for temporary files. The random segment
// inserted by os.CreateTemp guarantees that concurrent sessions never reuse or
// overwrite each other's files.
const tempFilePattern = "gum.*.md"

// noticeStyle is used to render editor-related status hints.
var noticeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))

type keymap struct {
	textarea.KeyMap
	Submit       key.Binding
	Quit         key.Binding
	Abort        key.Binding
	OpenInEditor key.Binding
}

// FullHelp implements help.KeyMap.
func (k keymap) FullHelp() [][]key.Binding { return nil }

// ShortHelp implements help.KeyMap.
func (k keymap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.InsertNewline,
		k.OpenInEditor,
		k.Submit,
	}
}

func defaultKeymap() keymap {
	km := textarea.DefaultKeyMap()
	km.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "insert newline"),
	)
	return keymap{
		KeyMap: km,
		Quit: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "quit"),
		),
		Abort: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "cancel"),
		),
		OpenInEditor: key.NewBinding(
			key.WithKeys("ctrl+e"),
			key.WithHelp("ctrl+e", "open editor"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "submit"),
		),
	}
}

type model struct {
	autoWidth   bool
	header      string
	headerStyle lipgloss.Style
	quitting    bool
	submitted   bool
	textarea    textarea.Model
	showHelp    bool
	help        help.Model
	keymap      keymap
	padding     []int
	temps       *tempFiles
	notice      string
}

func (m model) Init() tea.Cmd { return textarea.Blink }

func (m model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	var parts []string

	// Display the header above the text area if it is not empty.
	if m.header != "" {
		parts = append(parts, m.headerStyle.Render(m.header))
	}
	parts = append(parts, m.textarea.View())
	if m.notice != "" {
		parts = append(parts, noticeStyle.Render(m.notice))
	}
	if m.showHelp {
		parts = append(parts, "", m.help.View(m.keymap))
	}
	v := tea.NewView(lipgloss.NewStyle().
		Padding(m.padding...).
		Render(lipgloss.JoinVertical(
			lipgloss.Left,
			parts...,
		)))
	v.ReportFocus = true
	return v
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if m.autoWidth {
			m.textarea.SetWidth(msg.Width - m.padding[1] - m.padding[3])
		}
	case tea.FocusMsg, tea.BlurMsg:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	case startEditorMsg:
		return m, openEditor(m.temps, msg.path, msg.lineno)
	case editorFinishedMsg:
		m.notice = editorNotice(msg)
		if msg.outcome == editorSucceeded {
			// Only a successful editor run may replace the content.
			m.textarea.SetValue(msg.content)
		}
		return m, nil
	case tea.KeyPressMsg:
		km := m.keymap
		switch {
		case key.Matches(msg, km.Abort):
			m.quitting = true
			return m, tea.Interrupt
		case key.Matches(msg, km.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, km.Submit):
			m.quitting = true
			m.submitted = true
			return m, tea.Quit
		case key.Matches(msg, km.OpenInEditor):
			if limit, reached := editorLimitReached(&m.textarea); reached {
				// Refuse to open the editor once the content already fills the
				// character limit: an editor round-trip could only come back
				// truncated. Nothing is created or discarded.
				m.notice = fmt.Sprintf(
					"editor disabled: content already reaches the character limit (%d)",
					limit,
				)
				return m, nil
			}
			return m, createTempFile(m.temps, m.textarea.Value(), m.textarea.Line()+1)
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// editorOutcome classifies the result of an external editor invocation so that
// each failure path can present its own hint.
type editorOutcome int

const (
	// editorSucceeded means the editor exited normally and its content was
	// read back successfully.
	editorSucceeded editorOutcome = iota
	// editorStartFailed means the editor process could not be started (for
	// example because it does not exist) or no editor could be selected.
	editorStartFailed
	// editorExitFailed means the editor ran but exited with a non-zero status
	// or was terminated by a signal.
	editorExitFailed
	// editorReadFailed means the editor exited normally but its output could
	// not be read back.
	editorReadFailed
	// editorTempFailed means the temporary file could not be created or
	// written, so the editor is unavailable for this session.
	editorTempFailed
)

type startEditorMsg struct {
	path   string
	lineno int
}

type editorFinishedMsg struct {
	outcome editorOutcome
	content string
	err     error
}

// editorNotice turns an editor outcome into a user-visible hint. The hints for
// a start failure and a non-zero exit are intentionally different.
func editorNotice(msg editorFinishedMsg) string {
	switch msg.outcome {
	case editorSucceeded:
		return ""
	case editorStartFailed:
		return fmt.Sprintf("could not start editor: %v", msg.err)
	case editorExitFailed:
		return fmt.Sprintf(
			"editor exited unsuccessfully (%v); edited content was discarded",
			msg.err,
		)
	case editorReadFailed:
		return fmt.Sprintf(
			"could not read edited content: %v; kept previous content",
			msg.err,
		)
	case editorTempFailed:
		return fmt.Sprintf("editor unavailable: %v", msg.err)
	default:
		return ""
	}
}

// editorLimitReached reports whether the textarea already fills its character
// limit, in which case opening an external editor must be refused. A zero
// limit means unlimited and never blocks.
func editorLimitReached(t *textarea.Model) (limit int, reached bool) {
	if t.CharLimit <= 0 {
		return 0, false
	}
	return t.CharLimit, t.Length() >= t.CharLimit
}

// tempFiles tracks the temporary files created by a single write session so
// that every file is removed when its editor invocation ends and, as a safety
// net, when the session ends.
type tempFiles struct {
	mu    sync.Mutex
	paths []string
}

// add records a newly created temporary file.
func (t *tempFiles) add(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paths = append(t.paths, path)
}

// remove deletes path and forgets about it. It is idempotent: a file that has
// already been removed, for example externally, is not an error. A removal
// failure only produces a hint and never changes the submitted content or the
// exit code.
func (t *tempFiles) remove(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "gum: warning: could not remove temporary file %s: %v\n", path, err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for i, p := range t.paths {
		if p == path {
			t.paths = append(t.paths[:i], t.paths[i+1:]...)
			return
		}
	}
}

// cleanup removes every tracked temporary file. It is the session-level
// safety net for cancellation, interrupts and other paths that never run the
// editor callback. It is idempotent and never returns an error.
func (t *tempFiles) cleanup() {
	t.mu.Lock()
	paths := append([]string(nil), t.paths...)
	t.paths = nil
	t.mu.Unlock()

	for _, path := range paths {
		t.remove(path)
	}
}

// newTempFile creates a temporary file in dir (the default temporary directory
// when empty) and writes content to it exactly once. Files created this way
// are restricted to owner read/write permissions (0600).
func newTempFile(dir, content string) (string, error) {
	f, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	path := f.Name()

	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temporary file: %w", err)
	}
	return path, nil
}

// createTempFile prepares a temporary file for the editor and returns a
// startEditorMsg on success. Any file left over from a previous editor
// invocation is removed before a new one is created, so opening the editor
// twice never leaves the first file behind.
func createTempFile(temps *tempFiles, content string, lineno int) tea.Cmd {
	return func() tea.Msg {
		temps.cleanup()

		path, err := newTempFile("", content)
		if err != nil {
			return editorFinishedMsg{outcome: editorTempFailed, err: err}
		}
		temps.add(path)
		return startEditorMsg{path: path, lineno: lineno}
	}
}

// finishEditor is the shared completion logic of an editor invocation. The
// temporary file is always removed before returning, regardless of the
// outcome, and the original content is only replaced on a clean success.
func finishEditor(temps *tempFiles, path string, runErr error) editorFinishedMsg {
	if runErr != nil {
		temps.remove(path)

		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// A non-zero status (including death by signal) abandons the
			// edit without touching the existing content.
			return editorFinishedMsg{outcome: editorExitFailed, err: runErr}
		}
		// The editor could not be started at all.
		return editorFinishedMsg{outcome: editorStartFailed, err: runErr}
	}

	bts, err := os.ReadFile(path)
	// Cleanup happens even when the file cannot be read back, and an empty
	// read must never be mistaken for a successful submission.
	temps.remove(path)
	if err != nil {
		return editorFinishedMsg{outcome: editorReadFailed, err: err}
	}
	return editorFinishedMsg{outcome: editorSucceeded, content: string(bts)}
}

// openEditor runs the external editor on path, keeping the editor resolution
// and launch mechanics untouched.
func openEditor(temps *tempFiles, path string, lineno int) tea.Cmd {
	cmd, err := editor.Cmd(
		"Gum",
		path,
		editor.LineNumber(lineno),
		editor.EndOfLine(),
	)
	if err != nil {
		return func() tea.Msg { return finishEditor(temps, path, err) }
	}
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		return finishEditor(temps, path, runErr)
	})
}
