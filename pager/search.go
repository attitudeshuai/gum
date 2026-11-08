package pager

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

type search struct {
	active           bool
	input            textinput.Model
	query            *regexp.Regexp
	matchIndex       int
	matchLipglossStr string
	matchString      string
}

func (s *search) new() {
	input := textinput.New()
	input.Placeholder = "search"
	input.Prompt = "/"
	styles := input.Styles()
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	input.SetStyles(styles)
	s.input = input
}

func (s *search) Begin() {
	s.new()
	s.active = true
	s.input.Focus()
}

// Execute find all lines in the model with a match.
func (s *search) Execute(m *model) {
	defer s.Done()
	if s.input.Value() == "" {
		s.query = nil
		return
	}

	var err error
	s.query, err = regexp.Compile(s.input.Value())
	if err != nil {
		s.query = nil
		return
	}
	query := regexp.MustCompile(fmt.Sprintf("(%s)", s.query.String()))
	m.content = query.ReplaceAllString(m.content, m.matchStyle.Render("$1"))

	// Recompile the regex to match the an replace the highlights.
	leftPad, _ := lipglossPadding(m.matchStyle)
	matchingString := regexp.QuoteMeta(m.matchStyle.Render()[:leftPad]) + s.query.String() + regexp.QuoteMeta(m.matchStyle.Render()[leftPad:])
	s.query, err = regexp.Compile(matchingString)
	if err != nil {
		s.query = nil
	}
}

func (s *search) Done() {
	s.active = false

	// To account for the first match is always executed.
	s.matchIndex = -1
}

func (s *search) NextMatch(m *model) {
	// Check that we are within bounds.
	if s.query == nil {
		return
	}

	// Remove previous highlight.
	m.content = strings.Replace(m.content, s.matchLipglossStr, s.matchString, 1)

	// Highlight the next match.
	allMatches := s.query.FindAllStringIndex(m.content, -1)
	if len(allMatches) == 0 {
		return
	}

	leftPad, rightPad := lipglossPadding(m.matchStyle)
	s.matchIndex = (s.matchIndex + 1) % len(allMatches)
	match := allMatches[s.matchIndex]
	lhs := m.content[:match[0]]
	rhs := m.content[match[0]:]
	s.matchString = m.content[match[0]:match[1]]
	s.matchLipglossStr = m.matchHighlightStyle.Render(s.matchString[leftPad : len(s.matchString)-rightPad])
	m.content = lhs + strings.Replace(rhs, m.content[match[0]:match[1]], s.matchLipglossStr, 1)

	s.gotoMatchLine(m)
}

func (s *search) PrevMatch(m *model) {
	// Check that we are within bounds.
	if s.query == nil {
		return
	}

	// Remove previous highlight.
	m.content = strings.Replace(m.content, s.matchLipglossStr, s.matchString, 1)

	// Highlight the previous match.
	allMatches := s.query.FindAllStringIndex(m.content, -1)
	if len(allMatches) == 0 {
		return
	}

	s.matchIndex = (s.matchIndex - 1) % len(allMatches)
	if s.matchIndex < 0 {
		s.matchIndex = len(allMatches) - 1
	}

	leftPad, rightPad := lipglossPadding(m.matchStyle)
	match := allMatches[s.matchIndex]
	lhs := m.content[:match[0]]
	rhs := m.content[match[0]:]
	s.matchString = m.content[match[0]:match[1]]
	s.matchLipglossStr = m.matchHighlightStyle.Render(s.matchString[leftPad : len(s.matchString)-rightPad])
	m.content = lhs + strings.Replace(rhs, m.content[match[0]:match[1]], s.matchLipglossStr, 1)

	s.gotoMatchLine(m)
}

// gotoMatchLine scrolls the viewport so the highlighted match is visible.
// The match line is derived from wrapContent, which uses the exact same
// wrapping and termination rules as the view renderer, so match positions
// always agree with the displayed line numbers.
func (s *search) gotoMatchLine(m *model) {
	var line int
	formatStr := wrapContent(m.content, m.maxWidth, m.softWrap)
	index := strings.Index(formatStr, s.matchLipglossStr)
	if index != -1 {
		line = strings.Count(formatStr[:index], "\n")
	}

	// Only update if the match is not within the viewport.
	if index != -1 && (line > m.viewport.YOffset()-1+m.viewport.VisibleLineCount()-1 || line < m.viewport.YOffset()) {
		m.viewport.SetYOffset(line)
	}
}

// lipglossPadding calculates how much padding a string is given by a style.
func lipglossPadding(style lipgloss.Style) (int, int) {
	render := style.Render(" ")
	before := strings.Index(render, " ")
	after := len(render) - len(" ") - before
	return before, after
}
