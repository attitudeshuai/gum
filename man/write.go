package man

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
)

const (
	// mainPageFile is the file name of the overview page. It must stay stable
	// so existing packaging scripts keep working.
	mainPageFile = "gum.1"

	// file mode of generated pages (readable by everyone, writable by owner).
	pageMode os.FileMode = 0o644
)

// plannedPage is one rendered page and the stable file name it is installed
// under.
type plannedPage struct {
	filename string
	content  string
}

// planPages builds the ordered set of pages to install. The main overview page
// always comes first, followed by one page per visible command in model
// order, so repeated runs plan the exact same file names in the same order.
// When only is non-empty, just that command's page is planned.
func planPages(app *kong.Application, date, only string) ([]plannedPage, error) {
	if only != "" {
		command, err := findCommand(app, only)
		if err != nil {
			return nil, err
		}
		return []plannedPage{{
			filename: pageName(command) + ".1",
			content:  renderCommandPage(command, date),
		}}, nil
	}

	pages := []plannedPage{{
		filename: mainPageFile,
		content:  renderMainPage(app, date),
	}}
	for _, command := range visibleCommands(app) {
		pages = append(pages, plannedPage{
			filename: pageName(command) + ".1",
			content:  renderCommandPage(command, date),
		})
	}
	return pages, nil
}

// writePages installs every planned page into dir. Each file is written to a
// temporary file in the same directory and then atomically renamed into
// place, so a failure never leaves a partially written page behind.
func writePages(dir string, pages []plannedPage) error {
	if err := ensureWritableDir(dir); err != nil {
		return err
	}
	for _, page := range pages {
		target := filepath.Join(dir, page.filename)
		if err := atomicWrite(target, page.content); err != nil {
			return err
		}
	}
	return nil
}

// ensureWritableDir verifies that dir exists, is a directory and is writable,
// before any page is touched.
func ensureWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot write man pages to %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cannot write man pages to %q: not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".gum-man-probe-*")
	if err != nil {
		return fmt.Errorf("cannot write man pages to %q: %w", dir, err)
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return nil
}

// atomicWrite writes content to target via a temporary file in the same
// directory followed by an atomic rename.
func atomicWrite(target, content string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".gum-man-*")
	if err != nil {
		return fmt.Errorf("cannot create man page %q: %w", target, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = tmp.Chmod(pageMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write man page %q: %w", target, err)
	}
	if _, err = tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write man page %q: %w", target, err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write man page %q: %w", target, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("cannot write man page %q: %w", target, err)
	}
	if err = os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("cannot install man page %q: %w", target, err)
	}
	return nil
}
