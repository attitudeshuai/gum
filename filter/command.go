package filter

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/gum/v2/internal/files"
	"charm.land/gum/v2/internal/stdin"
	"charm.land/gum/v2/internal/timeout"
	"charm.land/gum/v2/internal/tty"
	"charm.land/gum/v2/style"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// Run provides a shell script interface for filtering through options, powered
// by the textinput bubble.
func (o Options) Run() error {
	i := textinput.New()
	i.Focus()

	i.Prompt = o.Prompt
	styles := i.Styles()
	styles.Focused.Prompt = o.PromptStyle.ToLipgloss()
	styles.Blurred.Prompt = o.PromptStyle.ToLipgloss()
	styles.Focused.Placeholder = o.PlaceholderStyle.ToLipgloss()
	styles.Blurred.Placeholder = o.PlaceholderStyle.ToLipgloss()
	i.SetStyles(styles)
	i.Placeholder = o.Placeholder
	i.SetWidth(o.Width)

	v := viewport.New(viewport.WithWidth(o.Width), viewport.WithHeight(o.Height))

	if len(o.Options) == 0 {
		if input, _ := stdin.Read(stdin.StripANSI(o.StripANSI)); input != "" {
			o.Options = strings.Split(input, o.InputDelimiter)
		} else {
			o.Options = files.List()
		}
	}

	if len(o.Options) == 0 {
		return errors.New("no options provided, see `gum filter --help`")
	}

	ctx, cancel := timeout.Context(o.Timeout)
	defer cancel()

	options := []tea.ProgramOption{
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	}

	var matches []fuzzy.Match
	if o.Value != "" {
		i.SetValue(o.Value)
	}

	// Keep one entry per option, in list order. Selection is tracked by the
	// position in these slices, so repeated options keep independent
	// identities instead of being merged by text in a map.
	choices := make([]string, 0, len(o.Options))
	filteringChoices := make([]string, 0, len(o.Options))
	for _, opt := range o.Options {
		s := ansi.Strip(opt)
		choices = append(choices, opt)
		filteringChoices = append(filteringChoices, s)
	}
	switch {
	case o.Value != "" && o.Fuzzy:
		matches = fuzzy.Find(o.Value, filteringChoices)
	case o.Value != "" && !o.Fuzzy:
		matches = exactMatches(o.Value, filteringChoices)
	default:
		matches = matchAll(filteringChoices)
	}

	if o.NoLimit {
		o.Limit = len(o.Options)
	}

	if o.SelectIfOne && len(matches) == 1 {
		tty.Println(matches[0].Str)
		return nil
	}

	km := defaultKeymap()
	if o.NoLimit || o.Limit > 1 {
		km.Toggle.SetEnabled(true)
		km.ToggleAndPrevious.SetEnabled(true)
		km.ToggleAndNext.SetEnabled(true)
		km.ToggleAll.SetEnabled(true)
	}
	top, right, bottom, left := style.ParsePadding(o.Padding)
	m := model{
		choices:               choices,
		filteringChoices:      filteringChoices,
		indicator:             o.Indicator,
		matches:               matches,
		header:                o.Header,
		textinput:             i,
		viewport:              &v,
		indicatorStyle:        o.IndicatorStyle.ToLipgloss(),
		selectedPrefixStyle:   o.SelectedPrefixStyle.ToLipgloss(),
		selectedPrefix:        o.SelectedPrefix,
		unselectedPrefixStyle: o.UnselectedPrefixStyle.ToLipgloss(),
		unselectedPrefix:      o.UnselectedPrefix,
		matchStyle:            o.MatchStyle.ToLipgloss(),
		headerStyle:           o.HeaderStyle.ToLipgloss(),
		textStyle:             o.TextStyle.ToLipgloss(),
		cursorTextStyle:       o.CursorTextStyle.ToLipgloss(),
		height:                o.Height,
		padding:               []int{top, right, bottom, left},
		selected:              make(map[int]struct{}),
		limit:                 o.Limit,
		reverse:               o.Reverse,
		fuzzy:                 o.Fuzzy,
		sort:                  o.Sort && o.FuzzySort,
		strict:                o.Strict,
		showHelp:              o.ShowHelp,
		keymap:                km,
		help:                  help.New(),
	}

	if len(o.Selected) > 0 {
		selected, cursor, count := preselect(matches, o.Selected, o.Limit)
		m.selected = selected
		m.cursor = cursor
		m.numSelected = count
	}

	tm, err := tea.NewProgram(m, options...).Run()
	if err != nil {
		return fmt.Errorf("unable to run filter: %w", err)
	}

	m = tm.(model)
	if !m.submitted {
		return errors.New("nothing selected")
	}

	// allSelections contains values only if limit is greater
	// than 1 or if flag --no-limit is passed, hence there is
	// no need to further checks
	if len(m.selected) > 0 {
		o.checkSelected(m)
	} else if len(m.matches) > m.cursor && m.cursor >= 0 {
		tty.Println(m.matches[m.cursor].Str)
	}

	return nil
}

// preselect applies the pre-selected texts onto the given matches. Texts are
// consumed one-to-one in list order, so repeated options land on their own
// positions instead of selecting every occurrence. In single-select mode no
// position is marked, only the cursor is moved to the last matching option.
// It returns the selected positions, the cursor position and the number of
// selected positions (never exceeding the limit).
func preselect(matches []fuzzy.Match, preselected []string, limit int) (map[int]struct{}, int, int) {
	isSelectAll := len(preselected) == 1 && preselected[0] == "*"

	// Remaining pre-selected occurrences keyed by option text.
	remaining := map[string]int{}
	if !isSelectAll {
		for _, s := range preselected {
			remaining[s]++
		}
	}

	selected := make(map[int]struct{})
	cursor, count := 0, 0
	for i, option := range matches {
		if count >= limit {
			break
		}
		if !isSelectAll && remaining[option.Str] == 0 {
			continue
		}
		if limit == 1 {
			// Single selection only hovers the cursor over the last matching
			// option without marking anything.
			cursor = i
			continue
		}
		if !isSelectAll {
			remaining[option.Str]--
		}
		selected[option.Index] = struct{}{}
		count++
	}
	return selected, cursor, count
}

func (o Options) checkSelected(m model) {
	out := m.selectedOutput()
	tty.Println(strings.Join(out, o.OutputDelimiter))
}

// selectedOutput returns the selected choices in the order in which the
// options were provided. Selection is tracked by position, hence duplicate
// options are written independently and repeated runs produce byte-identical
// output instead of depending on map iteration order.
func (m model) selectedOutput() []string {
	out := make([]string, 0, m.numSelected)
	for i, choice := range m.filteringChoices {
		if _, ok := m.selected[i]; ok {
			out = append(out, choice)
		}
	}
	// The typed query itself (non-strict mode) has no list position; write it
	// after the list entries so the list-order part of the output is stable.
	if _, ok := m.selected[typedChoiceIndex]; ok {
		out = append(out, m.textinput.Value())
	}
	return out
}
