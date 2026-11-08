package choose

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/paginator"
	tea "charm.land/bubbletea/v2"
)

var (
	keyToggle    = tea.KeyPressMsg{Text: "x", Code: 'x'}
	keyDown      = tea.KeyPressMsg{Code: tea.KeyDown}
	keySelectAll = tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'}
	keyAbort     = tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'}
)

func countSelected(items []item) int {
	n := 0
	for _, it := range items {
		if it.selected {
			n++
		}
	}
	return n
}

func newTestModel(items []item, limit int) model {
	p := paginator.New()
	p.PerPage = 10
	p.SetTotalPages((len(items) + 9) / 10)
	km := defaultKeymap()
	km.Toggle.SetEnabled(true)
	km.ToggleAll.SetEnabled(true)
	return model{
		items:       items,
		limit:       limit,
		height:      10,
		paginator:   p,
		keymap:      km,
		numSelected: countSelected(items),
	}
}

func press(t *testing.T, m model, msg tea.KeyPressMsg) model {
	t.Helper()
	tm, _ := m.Update(msg)
	return tm.(model)
}

func TestBuildItemsPreselectsByPosition(t *testing.T) {
	options := []string{"a", "b", "a"}

	// A single pre-selected text lands on the first matching position only.
	items, _, order, n := buildItems(options, options, []string{"a"}, 3)
	if n != 1 || order != 1 {
		t.Fatalf("expected 1 pre-selected item, got %d (order %d)", n, order)
	}
	if !items[0].selected || items[1].selected || items[2].selected {
		t.Fatalf("expected only position 0 selected, got %+v", items)
	}

	// Two references consume the two occurrences independently.
	items, _, _, n = buildItems(options, options, []string{"a", "a"}, 3)
	if n != 2 || !items[0].selected || items[1].selected || !items[2].selected {
		t.Fatalf("expected positions 0 and 2 selected, got %+v (n=%d)", items, n)
	}
}

func TestBuildItemsSelectAllAndLimit(t *testing.T) {
	options := []string{"a", "b", "a", "c"}

	items, _, _, n := buildItems(options, options, []string{"*"}, 2)
	if n != 2 {
		t.Fatalf("expected limit to cap pre-selection at 2, got %d", n)
	}
	if !items[0].selected || !items[1].selected || items[2].selected || items[3].selected {
		t.Fatalf("expected only the first two positions selected, got %+v", items)
	}

	items, _, _, n = buildItems(options, options, []string{"*"}, len(options)+1)
	if n != 4 {
		t.Fatalf("expected all 4 positions selected, got %d", n)
	}
}

func TestBuildItemsBlankOption(t *testing.T) {
	options := []string{"a", "", "a"}
	items, _, _, n := buildItems(options, options, []string{""}, 3)
	if n != 1 || !items[1].selected || items[1].value != "" {
		t.Fatalf("expected the blank position to be selected with an empty value, got %+v", items)
	}
}

func TestBuildItemsSingleSelectMovesCursorOnly(t *testing.T) {
	options := []string{"a", "b", "a"}

	items, startingIndex, _, n := buildItems(options, options, []string{"b"}, 1)
	if n != 0 || startingIndex != 1 {
		t.Fatalf("expected cursor at position 1 and no selection, got index %d n %d", startingIndex, n)
	}
	for _, it := range items {
		if it.selected {
			t.Fatalf("single select must not mark items, got %+v", items)
		}
	}

	// Duplicate match keeps the legacy behavior: cursor lands on the last match.
	_, startingIndex, _, _ = buildItems(options, options, []string{"a"}, 1)
	if startingIndex != 2 {
		t.Fatalf("expected cursor at last occurrence (2), got %d", startingIndex)
	}
}

func TestBuildItemsKeepsDuplicateLabelValues(t *testing.T) {
	labels := []string{"k", "k"}
	values := []string{"v1", "v2"}
	items, _, _, n := buildItems(labels, values, []string{"k"}, 3)
	if n != 1 || !items[0].selected || items[0].value != "v1" || items[1].value != "v2" {
		t.Fatalf("expected first duplicate selected with its own value, got %+v (n=%d)", items, n)
	}
}

func TestToggleTreatsDuplicatesAsIndependentPositions(t *testing.T) {
	options := []string{"a", "b", "a"}
	m := newTestModel(mustBuild(t, options, options, nil, 3), 3)

	m = press(t, m, keyToggle) // toggle position 0
	if m.numSelected != 1 || !m.items[0].selected || m.items[2].selected {
		t.Fatalf("expected only position 0 selected, got %+v", m.items)
	}
	if got := strings.Join(m.selectedValues(), "\n"); got != "a" {
		t.Fatalf("expected a single output line %q, got %q", "a", got)
	}

	m = press(t, m, keyDown) // index 1
	m = press(t, m, keyDown) // index 2
	m = press(t, m, keyToggle)
	if m.numSelected != 2 || !m.items[0].selected || !m.items[2].selected {
		t.Fatalf("expected both duplicate positions selected, got %+v", m.items)
	}
	if got := strings.Join(m.selectedValues(), "\n"); got != "a\na" {
		t.Fatalf("expected two identical output lines, got %q", got)
	}

	// Cancelling one duplicate leaves the other one untouched.
	m = press(t, m, keyDown) // wraps back to index 0
	m = press(t, m, keyToggle)
	if m.numSelected != 1 || m.items[0].selected || !m.items[2].selected {
		t.Fatalf("expected only position 2 to stay selected, got %+v", m.items)
	}
	if got := strings.Join(m.selectedValues(), "\n"); got != "a" {
		t.Fatalf("expected one remaining output line, got %q", got)
	}
}

func TestLimitIsEnforcedByPosition(t *testing.T) {
	options := []string{"a", "b", "c", "d"}
	m := newTestModel(mustBuild(t, options, options, nil, 2), 2)

	m = press(t, m, keySelectAll)
	if m.numSelected != 2 {
		t.Fatalf("expected select all to stop at limit 2, got %d", m.numSelected)
	}
	if got := len(m.selectedValues()); got != 2 {
		t.Fatalf("expected 2 written lines, got %d", got)
	}

	// Attempting to select more positions must not change the selection.
	m = press(t, m, keyDown) // index 1, already selected
	m = press(t, m, keyDown) // index 2, not selected
	m = press(t, m, keyToggle)
	if m.numSelected != 2 || m.items[2].selected {
		t.Fatalf("expected limit to block new positions, got %+v", m.items)
	}
}

func TestReselectAfterDeselectKeepsListOrder(t *testing.T) {
	options := []string{"c", "b", "a", "b"}
	m := newTestModel(mustBuild(t, options, options, nil, 4), 4)

	m = press(t, m, keySelectAll)
	m = press(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'a'}) // second press deselects all
	if m.numSelected != 0 {
		t.Fatalf("expected deselect all to clear selection, got %d", m.numSelected)
	}

	// Re-select position 3 first, then position 1: both hold text "b".
	m.index = 3
	m = press(t, m, keyToggle)
	m.index = 1
	m = press(t, m, keyToggle)

	want := "b\nb"
	if got := strings.Join(m.selectedValues(), "\n"); got != want {
		t.Fatalf("expected output in list order %q, got %q", want, got)
	}
	if m.numSelected != 2 {
		t.Fatalf("expected 2 selected positions, got %d", m.numSelected)
	}
}

func TestBlankOptionIsSelectableAndWritten(t *testing.T) {
	options := []string{"a", "", "b"}
	m := newTestModel(mustBuild(t, options, options, nil, 3), 3)

	m.index = 1
	m = press(t, m, keyToggle)
	m.index = 0
	m = press(t, m, keyToggle)

	if got := strings.Join(m.selectedValues(), "\n"); got != "a\n" {
		t.Fatalf("expected blank line between outputs, got %q", got)
	}
}

func TestOutputIsDeterministicAcrossRuns(t *testing.T) {
	options := []string{"a", "b", "a", "", "c", "a"}
	first := ""
	for run := 0; run < 10; run++ {
		m := newTestModel(mustBuild(t, options, options, nil, 6), 6)
		m = press(t, m, keySelectAll)
		got := strings.Join(m.selectedValues(), "\n")
		if run == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d diverged: %q != %q", run, got, first)
		}
	}
	if want := strings.Join(options, "\n"); first != want {
		t.Fatalf("expected full list in list order, got %q want %q", first, want)
	}
}

func TestAbortLeavesNoSelectionAndNoOutput(t *testing.T) {
	options := []string{"a", "b", "a"}
	m := newTestModel(mustBuild(t, options, options, nil, 3), 3)

	tm, cmd := m.Update(keyAbort)
	m = tm.(model)
	if !m.quitting || m.submitted {
		t.Fatalf("expected abort to quit without submitting, got quitting=%v submitted=%v", m.quitting, m.submitted)
	}
	if _, ok := cmd().(tea.InterruptMsg); !ok {
		t.Fatalf("expected interrupt command, got %v", cmd())
	}
	if len(m.selectedValues()) != 0 {
		t.Fatalf("expected no output after abort, got %v", m.selectedValues())
	}

	// A fresh run over the same list starts from an empty selection.
	fresh := newTestModel(mustBuild(t, options, options, nil, 3), 3)
	if fresh.numSelected != 0 || len(fresh.selectedValues()) != 0 {
		t.Fatal("expected re-run to start with an empty selection")
	}
}

func mustBuild(t *testing.T, options, values, selected []string, limit int) []item {
	t.Helper()
	items, _, _, _ := buildItems(options, values, selected, limit)
	return items
}
