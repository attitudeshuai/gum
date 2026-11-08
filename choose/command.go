package choose

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/paginator"
	tea "charm.land/bubbletea/v2"
	"charm.land/gum/v2/internal/stdin"
	"charm.land/gum/v2/internal/timeout"
	"charm.land/gum/v2/internal/tty"
	"charm.land/gum/v2/style"
)

// Run provides a shell script interface for choosing between different through
// options.
func (o Options) Run() error {
	input, _ := stdin.Read(stdin.StripANSI(o.StripANSI))
	if len(o.Options) > 0 && len(o.Selected) == 0 {
		o.Selected = strings.Split(input, o.InputDelimiter)
	} else if len(o.Options) == 0 {
		if input == "" {
			return errors.New("no options provided, see `gum choose --help`")
		}
		o.Options = strings.Split(input, o.InputDelimiter)
	}

	// Keep the displayed labels and written values aligned position by
	// position. Duplicate options keep their own position instead of being
	// merged into a map keyed by their text.
	values := make([]string, 0, len(o.Options))
	// keep the labels in the user-provided order
	var labels []string
	for _, opt := range o.Options {
		if o.LabelDelimiter == "" {
			values = append(values, opt)
			continue
		}
		label, value, ok := strings.Cut(opt, o.LabelDelimiter)
		if !ok {
			return fmt.Errorf("invalid option format: %q", opt)
		}
		labels = append(labels, label)
		values = append(values, value)
	}
	if o.LabelDelimiter != "" {
		o.Options = labels
	}

	if o.SelectIfOne && len(o.Options) == 1 {
		fmt.Println(values[0])
		return nil
	}

	// We don't need to display prefixes if we are only picking one option.
	// Simply displaying the cursor is enough.
	if o.Limit == 1 && !o.NoLimit {
		o.SelectedPrefix = ""
		o.UnselectedPrefix = ""
		o.CursorPrefix = ""
	}

	if o.NoLimit {
		o.Limit = len(o.Options) + 1
	}

	if o.Ordered {
		// Sort labels and values together so positions stay aligned after
		// sorting. Stable sorting keeps repeated options in list order.
		permutation := make([]int, len(o.Options))
		for i := range permutation {
			permutation[i] = i
		}
		sort.SliceStable(permutation, func(a, b int) bool {
			return o.Options[permutation[a]] < o.Options[permutation[b]]
		})
		sortedOptions := make([]string, len(o.Options))
		sortedValues := make([]string, len(values))
		for i, idx := range permutation {
			sortedOptions[i] = o.Options[idx]
			sortedValues[i] = values[idx]
		}
		o.Options = sortedOptions
		values = sortedValues
	}

	items, startingIndex, currentOrder, currentSelected := buildItems(o.Options, values, o.Selected, o.Limit)

	// Use the pagination model to display the current and total number of
	// pages.
	top, right, bottom, left := style.ParsePadding(o.Padding)
	pager := paginator.New()
	pager.SetTotalPages((len(items) + o.Height - 1) / o.Height)
	pager.PerPage = o.Height
	pager.Type = paginator.Dots
	pager.KeyMap = paginator.KeyMap{}
	pager.Page = startingIndex / o.Height

	km := defaultKeymap()
	if o.NoLimit || o.Limit > 1 {
		km.Toggle.SetEnabled(true)
	}
	if o.NoLimit {
		km.ToggleAll.SetEnabled(true)
	}

	m := model{
		index:             startingIndex,
		currentOrder:      currentOrder,
		height:            o.Height,
		padding:           []int{top, right, bottom, left},
		cursor:            o.Cursor,
		header:            o.Header,
		selectedPrefix:    o.SelectedPrefix,
		unselectedPrefix:  o.UnselectedPrefix,
		cursorPrefix:      o.CursorPrefix,
		items:             items,
		limit:             o.Limit,
		paginator:         pager,
		cursorStyle:       o.CursorStyle.ToLipgloss(),
		headerStyle:       o.HeaderStyle.ToLipgloss(),
		itemStyle:         o.ItemStyle.ToLipgloss(),
		selectedItemStyle: o.SelectedItemStyle.ToLipgloss(),
		numSelected:       currentSelected,
		showHelp:          o.ShowHelp,
		help:              help.New(),
		keymap:            km,
	}

	ctx, cancel := timeout.Context(o.Timeout)
	defer cancel()

	// Disable Keybindings since we will control it ourselves.
	tm, err := tea.NewProgram(
		m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(ctx),
	).Run()
	if err != nil {
		return fmt.Errorf("unable to pick selection: %w", err)
	}
	m = tm.(model)
	if !m.submitted {
		return errors.New("nothing selected")
	}
	if o.Ordered && o.Limit > 1 {
		sort.Slice(m.items, func(i, j int) bool {
			return m.items[i].order < m.items[j].order
		})
	}

	tty.Println(strings.Join(m.selectedValues(), o.OutputDelimiter))
	return nil
}

// buildItems turns the provided options into items and applies the
// pre-selected texts. Pre-selection is matched by position: every pre-selected
// text consumes one occurrence in list order, so repeated options map to their
// own positions instead of being merged by text. It returns the items, the
// initial cursor position, the next selection order counter and the number of
// pre-selected items.
func buildItems(options, values, selected []string, limit int) (items []item, startingIndex, currentOrder, currentSelected int) {
	isSelectAll := len(selected) == 1 && selected[0] == "*"

	// Remaining pre-selected occurrences keyed by option text.
	remaining := map[string]int{}
	if len(selected) > 0 && !isSelectAll {
		for _, s := range selected {
			remaining[s]++
		}
	}

	items = make([]item, len(options))
	for i, option := range options {
		var order int
		_, isPreSelected := remaining[option]
		// Check if the option should be selected.
		isSelected := len(selected) > 0 && currentSelected < limit && (isSelectAll || isPreSelected)
		// If the option is selected then increment the current selected count.
		if isSelected {
			if limit == 1 {
				// When the user can choose only one option don't select the option but
				// start with the cursor hovering over it.
				startingIndex = i
				isSelected = false
			} else {
				remaining[option]--
				if remaining[option] == 0 {
					delete(remaining, option)
				}
				currentSelected++
				order = currentOrder
				currentOrder++
			}
		}
		items[i] = item{text: option, value: values[i], selected: isSelected, order: order}
	}
	return items, startingIndex, currentOrder, currentSelected
}
