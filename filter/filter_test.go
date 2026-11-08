package filter

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

func TestMatchedRanges(t *testing.T) {
	for name, tt := range map[string]struct {
		in  []int
		out [][2]int
	}{
		"empty": {
			in:  []int{},
			out: [][2]int{},
		},
		"one char": {
			in:  []int{1},
			out: [][2]int{{1, 1}},
		},
		"2 char range": {
			in:  []int{1, 2},
			out: [][2]int{{1, 2}},
		},
		"multiple char range": {
			in:  []int{1, 2, 3, 4, 5, 6},
			out: [][2]int{{1, 6}},
		},
		"multiple char ranges": {
			in:  []int{1, 2, 3, 5, 6, 10, 11, 12, 13, 23, 24, 40, 42, 43, 45, 52},
			out: [][2]int{{1, 3}, {5, 6}, {10, 13}, {23, 24}, {40, 40}, {42, 43}, {45, 45}, {52, 52}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			match := matchedRanges(tt.in)
			if !reflect.DeepEqual(match, tt.out) {
				t.Errorf("expected %v, got %v", tt.out, match)
			}
		})
	}
}

func newFilterModel(list []string, limit int) model {
	return model{
		choices:          append([]string(nil), list...),
		filteringChoices: append([]string(nil), list...),
		matches:          matchAll(list),
		selected:         map[int]struct{}{},
		limit:            limit,
		keymap:           defaultKeymap(),
	}
}

func selectedPositions(m model) map[int]bool {
	out := map[int]bool{}
	for p := range m.selected {
		out[p] = true
	}
	return out
}

func TestMatchAllCarriesPositions(t *testing.T) {
	matches := matchAll([]string{"a", "", "a"})
	want := []fuzzy.Match{
		{Str: "a", Index: 0},
		{Str: "", Index: 1},
		{Str: "a", Index: 2},
	}
	if !reflect.DeepEqual(matches, want) {
		t.Fatalf("expected matches with list positions, got %+v", matches)
	}
}

func TestSelfMatchedIndexes(t *testing.T) {
	if got := selfMatchedIndexes("abc"); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("expected [0 1 2], got %v", got)
	}
	// Multi-byte runes must report byte offsets, like the matchers do.
	if got := selfMatchedIndexes("éa"); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("expected byte offsets [0 2], got %v", got)
	}
}

func TestPreselectLandsOnPositions(t *testing.T) {
	list := []string{"a", "b", "a", ""}
	matches := matchAll(list)

	tests := []struct {
		name        string
		preselected []string
		limit       int
		want        map[int]bool
		wantCursor  int
		wantCount   int
	}{
		{name: "one reference selects first occurrence", preselected: []string{"a"}, limit: 3, want: map[int]bool{0: true}, wantCount: 1},
		{name: "two references select both occurrences", preselected: []string{"a", "a"}, limit: 3, want: map[int]bool{0: true, 2: true}, wantCount: 2},
		{name: "blank matches the blank position", preselected: []string{""}, limit: 3, want: map[int]bool{3: true}, wantCount: 1},
		{name: "select all marks every position", preselected: []string{"*"}, limit: 4, want: map[int]bool{0: true, 1: true, 2: true, 3: true}, wantCount: 4},
		{name: "select all is capped by the limit", preselected: []string{"*"}, limit: 2, want: map[int]bool{0: true, 1: true}, wantCount: 2},
		{name: "single select only moves the cursor", preselected: []string{"b"}, limit: 1, want: map[int]bool{}, wantCursor: 1, wantCount: 0},
		{name: "unknown text selects nothing", preselected: []string{"z"}, limit: 3, want: map[int]bool{}, wantCount: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, cursor, count := preselect(matches, tt.preselected, tt.limit)
			m := model{selected: selected}
			if got := selectedPositions(m); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected positions %v, got %v", tt.want, got)
			}
			if count != tt.wantCount {
				t.Fatalf("expected count %d, got %d", tt.wantCount, count)
			}
			if cursor != tt.wantCursor {
				t.Fatalf("expected cursor %d, got %d", tt.wantCursor, cursor)
			}
		})
	}
}

func TestToggleSelectionTreatsDuplicatesAsPositions(t *testing.T) {
	m := newFilterModel([]string{"a", "b", "a"}, 3)

	m.cursor = 0
	m.ToggleSelection()
	if m.numSelected != 1 || !selectedPositions(m)[0] || selectedPositions(m)[2] {
		t.Fatalf("expected only position 0 selected, got %v", selectedPositions(m))
	}
	if got := strings.Join(m.selectedOutput(), "\n"); got != "a" {
		t.Fatalf("expected one output line %q, got %q", "a", got)
	}

	m.cursor = 2
	m.ToggleSelection()
	if m.numSelected != 2 || !selectedPositions(m)[0] || !selectedPositions(m)[2] {
		t.Fatalf("expected both duplicate positions selected, got %v", selectedPositions(m))
	}
	if got := strings.Join(m.selectedOutput(), "\n"); got != "a\na" {
		t.Fatalf("expected two identical output lines, got %q", got)
	}

	// Cancelling one duplicate leaves the other selected and counted.
	m.cursor = 0
	m.ToggleSelection()
	if m.numSelected != 1 || selectedPositions(m)[0] || !selectedPositions(m)[2] {
		t.Fatalf("expected only position 2 to remain selected, got %v", selectedPositions(m))
	}
	if got := strings.Join(m.selectedOutput(), "\n"); got != "a" {
		t.Fatalf("expected one remaining line, got %q", got)
	}
}

func TestFilterLimitIsEnforcedByPosition(t *testing.T) {
	m := newFilterModel([]string{"a", "b", "c"}, 2)
	m = m.selectAll()
	if m.numSelected != 2 || len(m.selectedOutput()) != 2 {
		t.Fatalf("expected select all to stop at limit, got count %d output %v", m.numSelected, m.selectedOutput())
	}

	m.cursor = 2
	m.ToggleSelection()
	if m.numSelected != 2 || selectedPositions(m)[2] {
		t.Fatalf("expected limit to block the new position, got %v", selectedPositions(m))
	}
}

func TestFilteringKeepsOriginalPositions(t *testing.T) {
	m := newFilterModel([]string{"a", "b", "a"}, 3)

	// Simulate a filtered view where only position 2 is visible. Toggling and
	// select-all must act on that original position, never on position 0.
	m.matches = []fuzzy.Match{{Str: "a", Index: 2}}
	m.cursor = 0
	m.ToggleSelection()
	if !selectedPositions(m)[2] || selectedPositions(m)[0] {
		t.Fatalf("expected toggle to hit position 2 only, got %v", selectedPositions(m))
	}

	m = m.deselectAll()
	m = m.selectAll()
	if !selectedPositions(m)[2] || selectedPositions(m)[0] {
		t.Fatalf("expected select all to mark visible positions only, got %v", selectedPositions(m))
	}
}

func TestSelectedOutputOrderWithBlanksAndDupes(t *testing.T) {
	list := []string{"a", "", "a", "b"}
	m := newFilterModel(list, 4)
	for _, p := range []int{3, 0, 2, 1} {
		m.selected[p] = struct{}{}
		m.numSelected++
	}

	want := "a\n\na\nb"
	first := ""
	for run := 0; run < 10; run++ {
		got := strings.Join(m.selectedOutput(), "\n")
		if run == 0 {
			first = got
			if got != want {
				t.Fatalf("expected list-order output %q, got %q", want, got)
			}
			continue
		}
		if got != first {
			t.Fatalf("run %d diverged: %q != %q", run, got, first)
		}
	}
	if m.numSelected != len(m.selectedOutput()) {
		t.Fatalf("selected count %d does not match written lines %d", m.numSelected, len(m.selectedOutput()))
	}
}

func TestSyntheticTypedChoiceIsWrittenLast(t *testing.T) {
	m := newFilterModel([]string{"a"}, 2)
	m.textinput = textinput.New()
	m.textinput.SetValue("q")

	m.selected[typedChoiceIndex] = struct{}{}
	m.numSelected = 1
	if got := strings.Join(m.selectedOutput(), "\n"); got != "q" {
		t.Fatalf("expected typed choice output %q, got %q", "q", got)
	}

	m.selected[0] = struct{}{}
	m.numSelected = 2
	if got := strings.Join(m.selectedOutput(), "\n"); got != "a\nq" {
		t.Fatalf("expected list entry before typed choice, got %q", got)
	}
}

func TestFilterAbortLeavesNoSelectionAndNoOutput(t *testing.T) {
	m := newFilterModel([]string{"a", "b", "a"}, 3)
	m.textinput = textinput.New()

	tm, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	got := tm.(model)
	if !got.quitting || got.submitted {
		t.Fatalf("expected abort to quit without submitting, got quitting=%v submitted=%v", got.quitting, got.submitted)
	}
	if _, ok := cmd().(tea.InterruptMsg); !ok {
		t.Fatalf("expected interrupt command, got %v", cmd())
	}
	if len(got.selectedOutput()) != 0 {
		t.Fatalf("expected no output after abort, got %v", got.selectedOutput())
	}

	// Re-running with the same list starts from an empty selection.
	fresh := newFilterModel([]string{"a", "b", "a"}, 3)
	if fresh.numSelected != 0 || len(fresh.selectedOutput()) != 0 {
		t.Fatal("expected re-run to start with an empty selection")
	}
}

func TestChooseAndFilterShareSelectionIdentity(t *testing.T) {
	// Both commands must resolve the same pre-selected text to the same
	// position, including when the text repeats and blanks are present.
	list := []string{"a", "b", "a", ""}
	matches := matchAll(list)

	selected, _, count := preselect(matches, []string{"a", "a"}, 4)
	got := selectedPositions(model{selected: selected})
	if count != 2 || !got[0] || !got[2] || got[1] || got[3] {
		t.Fatalf("expected filter positions {0 2}, got %v (count %d)", got, count)
	}

	selected, _, count = preselect(matches, []string{""}, 4)
	got = selectedPositions(model{selected: selected})
	if count != 1 || !got[3] {
		t.Fatalf("expected filter blank position {3}, got %v (count %d)", got, count)
	}
}

func TestByteToChar(t *testing.T) {
	stStr := "\x1b[90m\ue615\x1b[39m \x1b[3m\x1b[32mDow\x1b[0m\x1b[90m\x1b[39m\x1b[3wnloads"
	str := " Downloads"
	rng := [2]int{4, 7}
	expect := "Dow"

	if got := str[rng[0]:rng[1]]; got != expect {
		t.Errorf("expected %q, got %q", expect, got)
	}

	start, stop := bytePosToVisibleCharPos(str, rng)
	if got := ansi.Strip(ansi.Cut(stStr, start, stop)); got != expect {
		t.Errorf("expected %+q, got %+q", expect, got)
	}
}
