// Package pager provides a pager (similar to less) for the terminal.
//
// $ cat file.txt | gum pager
package pager

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

const (
	// minLineNumberWidth keeps the historical "   1 │ " look for files
	// with fewer than four-digit line counts.
	minLineNumberWidth = 4
	// gutterSeparator is rendered between the line number and the content.
	gutterSeparator = " │ "
	// tabSpacing is the fixed column width used to expand tabs.
	tabSpacing = "    "
)

type keymap struct {
	Home,
	End,
	Search,
	NextMatch,
	PrevMatch,
	Abort,
	Quit,
	ConfirmSearch,
	CancelSearch key.Binding
}

// FullHelp implements help.KeyMap.
func (k keymap) FullHelp() [][]key.Binding {
	return nil
}

// ShortHelp implements help.KeyMap.
func (k keymap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("↓↑", "navigate"),
		),
		k.Quit,
		k.Search,
		k.NextMatch,
		k.PrevMatch,
	}
}

func defaultKeymap() keymap {
	return keymap{
		Home: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("h", "home"),
		),
		End: key.NewBinding(
			key.WithKeys("G", "end"),
			key.WithHelp("G", "end"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		PrevMatch: key.NewBinding(
			key.WithKeys("p", "N"),
			key.WithHelp("N", "previous match"),
		),
		NextMatch: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "next match"),
		),
		Abort: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "abort"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "esc"),
			key.WithHelp("esc", "quit"),
		),
		ConfirmSearch: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		CancelSearch: key.NewBinding(
			key.WithKeys("ctrl+c", "ctrl+d", "esc"),
			key.WithHelp("ctrl+c", "cancel"),
		),
	}
}

type model struct {
	content             string
	origContent         string
	viewport            viewport.Model
	help                help.Model
	showLineNumbers     bool
	lineNumberStyle     lipgloss.Style
	softWrap            bool
	search              search
	matchStyle          lipgloss.Style
	matchHighlightStyle lipgloss.Style
	maxWidth            int
	keymap              keymap
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.processText(msg)
	case tea.KeyPressMsg:
		return m.keyHandler(msg)
	}

	m.keymap.PrevMatch.SetEnabled(m.search.query != nil)
	m.keymap.NextMatch.SetEnabled(m.search.query != nil)

	var cmd tea.Cmd
	m.search.input, cmd = m.search.input.Update(msg)
	return m, cmd
}

func (m *model) helpView() string {
	return m.help.View(m.keymap)
}

// currentSizeMsg rebuilds the window size message that describes the pager's
// current layout. It is used to reprocess the content without changing any
// dimensions, so calling it repeatedly (for example while jumping between
// search matches) cannot accumulate offsets.
func (m model) currentSizeMsg() tea.WindowSizeMsg {
	return tea.WindowSizeMsg{
		Height: m.viewport.Height() + lipgloss.Height(m.helpView()),
		Width:  m.viewport.Width(),
	}
}

func (m *model) processText(msg tea.WindowSizeMsg) {
	height := msg.Height - lipgloss.Height(m.helpView())
	if height < 0 {
		height = 0
	}
	m.viewport.SetHeight(height)
	m.viewport.SetWidth(msg.Width)
	textStyle := lipgloss.NewStyle().Width(m.viewport.Width())
	var text strings.Builder

	// Determine max width of a line.
	m.maxWidth = m.viewport.Width()
	if m.softWrap {
		vpStyle := m.viewport.Style
		m.maxWidth -= vpStyle.GetHorizontalBorderSize() + vpStyle.GetHorizontalMargins() + vpStyle.GetHorizontalPadding()
	}

	// The line number slot grows with the total number of lines so that
	// five-digit (and larger) line numbers still line up with the content.
	numberWidth := 0
	continuationGutter := ""
	tildeGutter := ""
	if m.showLineNumbers {
		numberWidth = lineNumberSlotWidth(m.content)
		continuationGutter = strings.Repeat(" ", numberWidth+1) + "│ "
		tildeGutter = strings.Repeat(" ", numberWidth-1) + "~" + gutterSeparator
		if m.softWrap {
			m.maxWidth -= numberWidth + len(gutterSeparator)
		}
	}

	// A terminal narrower than the chrome leaves no room for text. Clamp
	// to one column so the wrapping loop always terminates: content is
	// truncated instead of stalling the program forever.
	if m.softWrap && m.maxWidth < 1 {
		m.maxWidth = 1
	}

	for i, line := range strings.Split(m.content, "\n") {
		segments := wrapLine(line, m.maxWidth, m.softWrap)
		for j, segment := range segments {
			if m.showLineNumbers {
				if j == 0 {
					text.WriteString(m.lineNumberStyle.Render(fmt.Sprintf("%*d"+gutterSeparator, numberWidth, i+1)))
				} else {
					text.WriteString(m.lineNumberStyle.Render(continuationGutter))
				}
			}
			text.WriteString(textStyle.Render(segment))
			text.WriteByte('\n')
		}
	}

	diffHeight := m.viewport.Height() - lipgloss.Height(text.String())
	if diffHeight > 0 && m.showLineNumbers {
		var remainingLines strings.Builder
		remainingLines.WriteString(tildeGutter)
		for range diffHeight - 1 {
			remainingLines.WriteByte('\n')
			remainingLines.WriteString(tildeGutter)
		}
		text.WriteString(m.lineNumberStyle.Render(remainingLines.String()))
	}
	m.viewport.SetContent(text.String())
}

// lineNumberSlotWidth returns the number of columns reserved for line
// numbers: at least minLineNumberWidth, or as many digits as the total
// number of lines requires.
func lineNumberSlotWidth(content string) int {
	totalLines := strings.Count(content, "\n") + 1
	width := len(strconv.Itoa(totalLines))
	if width < minLineNumberWidth {
		return minLineNumberWidth
	}
	return width
}

// wrapLine expands tabs and splits a single physical line into visual
// segments whose display width never exceeds maxWidth. The scan is a single
// linear pass over the line. Splitting happens on grapheme cluster
// boundaries, so wide and combining characters are never cut in two.
// Graphemes wider than the available slot (only possible when the slot is a
// single column) are visually truncated. The function always terminates:
// every iteration consumes at least one grapheme cluster.
func wrapLine(line string, maxWidth int, softWrap bool) []string {
	line = strings.ReplaceAll(line, "\t", tabSpacing)
	if !softWrap || maxWidth <= 0 {
		return []string{line}
	}
	if ansi.StringWidth(line) <= maxWidth {
		return []string{line}
	}

	var (
		segments []string
		cur      strings.Builder
		// carry holds escape sequences preceding a glyph that had to be
		// truncated; they belong to whatever segment comes next.
		carry    strings.Builder
		curWidth int
		pstate   = parser.GroundState
	)

	flush := func() {
		segments = append(segments, cur.String())
		cur.Reset()
		curWidth = 0
	}
	useCarry := func() {
		if carry.Len() > 0 {
			cur.WriteString(carry.String())
			carry.Reset()
		}
	}

	b := []byte(line)
	for i := 0; i < len(b); {
		state, action := parser.Table.Transition(pstate, b[i])
		if state == parser.Utf8State {
			cluster, width := ansi.FirstGraphemeCluster(b[i:], ansi.GraphemeWidth)
			i += len(cluster)
			switch {
			case width == 0:
				// Combining mark or other zero-width cluster: it must
				// stay attached to the preceding grapheme.
				useCarry()
				cur.Write(cluster)
			case width > maxWidth:
				// The glyph is wider than the entire slot. Flush any
				// pending content and truncate the glyph itself.
				if curWidth > 0 {
					flush()
				}
				if cur.Len() > 0 {
					carry.WriteString(cur.String())
					cur.Reset()
				}
			case curWidth > 0 && curWidth+width > maxWidth:
				flush()
				useCarry()
				cur.Write(cluster)
				curWidth = width
			default:
				useCarry()
				cur.Write(cluster)
				curWidth += width
			}
			pstate = parser.GroundState
			continue
		}

		switch action {
		case parser.PrintAction:
			if curWidth > 0 && curWidth+1 > maxWidth {
				flush()
			}
			useCarry()
			cur.WriteByte(b[i])
			curWidth++
		case parser.ExecuteAction:
			// Control characters occupy no columns.
			useCarry()
			cur.WriteByte(b[i])
		default:
			// Escape sequences occupy no columns and must survive.
			useCarry()
			cur.WriteByte(b[i])
		}
		if pstate != parser.Utf8State {
			pstate = state
		}
		i++
	}

	if curWidth > 0 {
		flush()
	}
	if len(segments) == 0 {
		// Every glyph was truncated (e.g. wide characters in a
		// one-column terminal); the physical line still renders once.
		segments = append(segments, carry.String())
	}
	return segments
}

// wrapContent applies the exact same segmentation used by the view renderer
// to the whole content, so search match positions map to the same visual
// line numbers that are displayed.
func wrapContent(content string, maxWidth int, softWrap bool) string {
	var text strings.Builder
	for _, line := range strings.Split(content, "\n") {
		for _, segment := range wrapLine(line, maxWidth, softWrap) {
			text.WriteString(segment)
			text.WriteByte('\n')
		}
	}
	return text.String()
}

func (m model) keyHandler(msg tea.KeyPressMsg) (model, tea.Cmd) {
	km := m.keymap
	var cmd tea.Cmd
	if m.search.active {
		switch {
		case key.Matches(msg, km.ConfirmSearch):
			if m.search.input.Value() != "" {
				m.content = m.origContent
				m.search.Execute(&m)

				// Trigger a view update to highlight the found matches.
				m.search.NextMatch(&m)
				m.processText(m.currentSizeMsg())
			} else {
				m.search.Done()
			}
		case key.Matches(msg, km.CancelSearch):
			m.search.Done()
		default:
			m.search.input, cmd = m.search.input.Update(msg)
		}
	} else {
		switch {
		case key.Matches(msg, km.Home):
			m.viewport.GotoTop()
		case key.Matches(msg, km.End):
			m.viewport.GotoBottom()
		case key.Matches(msg, km.Search):
			m.search.Begin()
			return m, textinput.Blink
		case key.Matches(msg, km.PrevMatch):
			m.search.PrevMatch(&m)
			m.processText(m.currentSizeMsg())
		case key.Matches(msg, km.NextMatch):
			m.search.NextMatch(&m)
			m.processText(m.currentSizeMsg())
		case key.Matches(msg, km.Quit):
			return m, tea.Quit
		case key.Matches(msg, km.Abort):
			return m, tea.Interrupt
		}
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

func (m model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.ReportFocus = true
	if m.search.active {
		v.SetContent(m.viewport.View() + "\n " + m.search.input.View())
		return v
	}

	v.SetContent(m.viewport.View() + "\n" + m.helpView())
	return v
}
