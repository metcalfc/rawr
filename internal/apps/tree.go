package apps

import (
	"context"
	"errors"
	"fmt"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxTreeRows = 10000

// safeText treats filenames, subprocess text, and file contents as inert text.
func safeText(value string) string { return ui.SafeText(value) }

type treeRow struct {
	path, name                   string
	depth                        int
	directory, symlink, expanded bool
}
type tree struct {
	root     string
	expanded map[string]bool
	rows     []treeRow
}

func newTree(root string) (*tree, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory: %s", root)
	}
	return &tree{root: root, expanded: map[string]bool{}}, nil
}

func (t *tree) inside(path string) (string, error) {
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(t.root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("selection points outside the project root")
	}
	return actual, nil
}

func (t *tree) refresh() error {
	t.rows = nil
	var firstErr error
	var walk func(string, int)
	walk = func(directory string, depth int) {
		if depth > 100 || len(t.rows) >= maxTreeRows {
			return
		}
		// Verify each expanded directory again; a previously safe path may have been replaced.
		actual, err := t.inside(directory)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		entries, limited, err := services.FileDirectory(t.root, actual, maxTreeRows-len(t.rows))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("cannot read %s: %w", directory, err)
			}
			return
		}
		if limited && firstErr == nil {
			firstErr = errors.New("tree limited to 10,000 visible entries; collapse a directory to browse further")
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			a, b := strings.ToLower(entries[i].Name()), strings.ToLower(entries[j].Name())
			if a == b {
				return entries[i].Name() < entries[j].Name()
			}
			return a < b
		})
		for _, entry := range entries {
			if len(t.rows) >= maxTreeRows {
				if firstErr == nil {
					firstErr = errors.New("tree limited to 10,000 visible entries; collapse a directory to browse further")
				}
				return
			}
			path := filepath.Join(directory, entry.Name())
			symlink := entry.Type()&os.ModeSymlink != 0
			expanded := entry.IsDir() && !symlink && t.expanded[path]
			t.rows = append(t.rows, treeRow{path: path, name: entry.Name(), depth: depth, directory: entry.IsDir(), symlink: symlink, expanded: expanded})
			if expanded {
				walk(path, depth+1)
			}
		}
	}
	walk(t.root, 0)
	return firstErr
}

func (t *tree) preview(path string) ([]string, error) {
	actual, err := t.inside(path)
	if err != nil {
		return nil, err
	}
	doc, err := services.ReadFileDocument(context.Background(), t.root, actual, false, false)
	if err != nil {
		return nil, err
	}
	return safeFileLines(doc.Lines), nil
}
