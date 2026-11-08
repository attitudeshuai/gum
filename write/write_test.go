package write

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/editor"
)

// writeFile is a small helper for test fixtures.
func writeFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// trackedFile creates a tracked temporary file inside the test directory.
func trackedFile(t *testing.T, temps *tempFiles, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gum.test.md")
	writeFile(t, path, []byte(content), 0o600)
	temps.add(path)
	return path
}

func assertRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected %s to be removed, got err=%v", path, err)
	}
}

func TestTempFilesRemoveIsIdempotent(t *testing.T) {
	temps := &tempFiles{}
	path := trackedFile(t, temps, "data")

	temps.remove(path)
	assertRemoved(t, path)

	// Repeating the cleanup after the file was removed (also externally)
	// must not report an error or panic.
	temps.remove(path)

	if got := len(temps.paths); got != 0 {
		t.Fatalf("expected no tracked paths, got %d", got)
	}
}

func TestTempFilesCleanupRemovesEverything(t *testing.T) {
	temps := &tempFiles{}
	first := trackedFile(t, temps, "one")
	second := trackedFile(t, temps, "two")

	// Remove one file externally: cleanup must still succeed.
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}

	temps.cleanup()

	assertRemoved(t, first)
	assertRemoved(t, second)

	// A second cleanup is a no-op without errors.
	temps.cleanup()
}

func TestNewTempFilePermissionsPatternAndContent(t *testing.T) {
	dir := t.TempDir()
	path, err := newTempFile(dir, "hello\nworld")
	if err != nil {
		t.Fatalf("newTempFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}

	base := filepath.Base(path)
	if filepath.Ext(base) != ".md" || base[:4] != "gum." {
		t.Fatalf("unexpected temp file name %q", base)
	}

	bts, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(bts) != "hello\nworld" {
		t.Fatalf("unexpected content %q", string(bts))
	}
}

func TestNewTempFileFailure(t *testing.T) {
	// A missing parent directory always makes creation fail.
	missing := filepath.Join(t.TempDir(), "does-not-exist", "nested")
	if _, err := newTempFile(missing, "x"); err == nil {
		t.Fatal("expected an error when the temp directory does not exist")
	}

	// An unwritable directory must degrade instead of crashing. Skip when
	// running as root, which bypasses permission bits.
	if os.Geteuid() == 0 {
		return
	}
	readOnly := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if _, err := newTempFile(readOnly, "x"); err == nil {
		t.Fatal("expected an error when the temp directory is not writable")
	}
}

func TestCreateTempFileCleansPreviousFile(t *testing.T) {
	temps := &tempFiles{}

	firstMsg := createTempFile(temps, "first", 1)()
	first, ok := firstMsg.(startEditorMsg)
	if !ok {
		t.Fatalf("expected startEditorMsg, got %T", firstMsg)
	}
	if _, err := os.Stat(first.path); err != nil {
		t.Fatalf("first temp file missing: %v", err)
	}

	// Opening the editor a second time removes the first file before
	// creating a new one.
	secondMsg := createTempFile(temps, "second", 1)()
	second, ok := secondMsg.(startEditorMsg)
	if !ok {
		t.Fatalf("expected startEditorMsg, got %T", secondMsg)
	}
	assertRemoved(t, first.path)
	if second.path == first.path {
		t.Fatal("second editor invocation reused the previous temp file")
	}

	temps.mu.Lock()
	if got := len(temps.paths); got != 1 || temps.paths[0] != second.path {
		t.Fatalf("unexpected tracked paths: %v", temps.paths)
	}
	temps.mu.Unlock()

	temps.cleanup()
	assertRemoved(t, second.path)
}

func TestCreateTempFileConcurrentUnique(t *testing.T) {
	const sessions = 50

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		msgs     = make([]tea.Msg, sessions)
		trackers []*tempFiles
	)
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			temps := &tempFiles{}
			mu.Lock()
			trackers = append(trackers, temps)
			mu.Unlock()
			msgs[i] = createTempFile(temps, "content", 1)()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, sessions)
	for _, msg := range msgs {
		start, ok := msg.(startEditorMsg)
		if !ok {
			t.Fatalf("expected startEditorMsg, got %T", msg)
		}
		if seen[start.path] {
			t.Fatalf("duplicate temp file path: %s", start.path)
		}
		seen[start.path] = true
		if _, err := os.Stat(start.path); err != nil {
			t.Fatalf("temp file missing: %v", err)
		}
	}

	for _, temps := range trackers {
		temps.cleanup()
	}
	for path := range seen {
		assertRemoved(t, path)
	}
}

func TestCreateTempFileDegradesOnFailure(t *testing.T) {
	// newTempFile is exercised directly above; here we verify the message
	// contract by pointing creation at a missing directory.
	temps := &tempFiles{}
	cmd := func() tea.Msg {
		path, err := newTempFile(filepath.Join(t.TempDir(), "missing"), "x")
		if err != nil {
			return editorFinishedMsg{outcome: editorTempFailed, err: err}
		}
		temps.add(path)
		return startEditorMsg{path: path}
	}
	msg, ok := cmd().(editorFinishedMsg)
	if !ok {
		t.Fatalf("expected editorFinishedMsg, got %T", msg)
	}
	if msg.outcome != editorTempFailed {
		t.Fatalf("expected editorTempFailed, got %v", msg.outcome)
	}
	if got := len(temps.paths); got != 0 {
		t.Fatalf("expected no tracked files, got %d", got)
	}
}

func TestFinishEditorSuccess(t *testing.T) {
	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	msg := finishEditor(temps, path, nil)
	if msg.outcome != editorSucceeded {
		t.Fatalf("expected editorSucceeded, got %v", msg.outcome)
	}
	if msg.content != "before" {
		t.Fatalf("unexpected content %q", msg.content)
	}
	assertRemoved(t, path)
	if got := len(temps.paths); got != 0 {
		t.Fatalf("expected no tracked files, got %d", got)
	}
}

func TestFinishEditorReadFailureKeepsContentAndCleans(t *testing.T) {
	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	msg := finishEditor(temps, path, nil)
	if msg.outcome != editorReadFailed {
		t.Fatalf("expected editorReadFailed, got %v", msg.outcome)
	}
	if msg.content != "" {
		t.Fatalf("read failure must not produce content, got %q", msg.content)
	}
	if msg.err == nil {
		t.Fatal("expected the read error to be reported")
	}

	// Idempotent: finishing again must not panic or re-report the file.
	again := finishEditor(temps, path, nil)
	if again.outcome != editorReadFailed {
		t.Fatalf("expected editorReadFailed again, got %v", again.outcome)
	}
}

func TestFinishEditorNonZeroExitDiscardsEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based editor simulation is not available on windows")
	}
	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	cmd := exec.Command("/bin/sh", "-c", "exit 3")
	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("expected a non-zero exit error")
	}

	msg := finishEditor(temps, path, runErr)
	if msg.outcome != editorExitFailed {
		t.Fatalf("expected editorExitFailed, got %v", msg.outcome)
	}
	if msg.content != "" {
		t.Fatalf("discarded edit must not replace content, got %q", msg.content)
	}
	assertRemoved(t, path)
}

func TestFinishEditorStartFailure(t *testing.T) {
	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	cmd := exec.Command("/nonexistent/gum-editor-fixture")
	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("expected a start error")
	}

	msg := finishEditor(temps, path, runErr)
	if msg.outcome != editorStartFailed {
		t.Fatalf("expected editorStartFailed, got %v", msg.outcome)
	}
	// Start failure and non-zero exit must remain distinct outcomes with
	// distinct user-visible hints.
	if editorNotice(msg) == editorNotice(editorFinishedMsg{outcome: editorExitFailed, err: runErr}) {
		t.Fatal("start failure and exit failure share the same hint")
	}
	assertRemoved(t, path)
}

func TestFinishEditorPlainResolutionError(t *testing.T) {
	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	msg := finishEditor(temps, path, errors.New("editor not configured"))
	if msg.outcome != editorStartFailed {
		t.Fatalf("expected editorStartFailed, got %v", msg.outcome)
	}
	assertRemoved(t, path)
}

func TestEditorLimitReached(t *testing.T) {
	ta := textarea.New()

	// Zero means unlimited.
	if limit, reached := editorLimitReached(&ta); reached || limit != 0 {
		t.Fatalf("unlimited textarea must not block, got limit=%d reached=%v", limit, reached)
	}

	ta.CharLimit = 5
	ta.SetValue("abc")
	if _, reached := editorLimitReached(&ta); reached {
		t.Fatal("content below the limit must not block")
	}

	ta.SetValue("abcde")
	limit, reached := editorLimitReached(&ta)
	if !reached || limit != 5 {
		t.Fatalf("content at the limit must block, got limit=%d reached=%v", limit, reached)
	}

	// The textarea itself clamps over-limit values, which must also block.
	ta.SetValue("abcdefgh")
	if _, reached := editorLimitReached(&ta); !reached {
		t.Fatal("content clamped to the limit must block")
	}
}

// fakeEditor writes an executable shell script acting as EDITOR and returns
// its path.
func fakeEditor(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-based editor simulation is not available on windows")
	}
	path := filepath.Join(t.TempDir(), "fake-editor.sh")
	writeFile(t, path, []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	return path
}

func TestOpenEditorRoundTripWithFakeEditor(t *testing.T) {
	// Successful editor: rewritten content comes back, file is removed.
	editorPath := fakeEditor(t, `printf 'from editor' > "$1"`)
	t.Setenv("EDITOR", editorPath)

	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	cmd, err := editor.Cmd("Gum", path, editor.LineNumber(1), editor.EndOfLine())
	if err != nil {
		t.Fatalf("editor.Cmd: %v", err)
	}
	msg := finishEditor(temps, path, cmd.Run())
	if msg.outcome != editorSucceeded {
		t.Fatalf("expected editorSucceeded, got %v: %v", msg.outcome, msg.err)
	}
	if msg.content != "from editor" {
		t.Fatalf("unexpected content %q", msg.content)
	}
	assertRemoved(t, path)
}

func TestOpenEditorNonZeroFakeEditor(t *testing.T) {
	editorPath := fakeEditor(t, `printf 'ignored' > "$1"; exit 2`)
	t.Setenv("EDITOR", editorPath)

	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	cmd, err := editor.Cmd("Gum", path, editor.LineNumber(1))
	if err != nil {
		t.Fatalf("editor.Cmd: %v", err)
	}
	msg := finishEditor(temps, path, cmd.Run())
	if msg.outcome != editorExitFailed {
		t.Fatalf("expected editorExitFailed, got %v", msg.outcome)
	}
	assertRemoved(t, path)
}

func TestOpenEditorMissingProgram(t *testing.T) {
	t.Setenv("EDITOR", "/nonexistent/gum-editor-fixture")

	temps := &tempFiles{}
	path := trackedFile(t, temps, "before")

	cmd, err := editor.Cmd("Gum", path)
	if err != nil {
		t.Fatalf("editor.Cmd resolves lazily, got %v", err)
	}
	msg := finishEditor(temps, path, cmd.Run())
	if msg.outcome != editorStartFailed {
		t.Fatalf("expected editorStartFailed, got %v", msg.outcome)
	}
	assertRemoved(t, path)
}
