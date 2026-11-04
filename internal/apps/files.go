package apps

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

type fileRow struct {
	path, name                                               string
	depth                                                    int
	directory, symlink, expanded, staged, deleted, untracked bool
	status                                                   string
}
type fileView struct {
	changes                    bool
	filtering                  bool
	query, previousQuery       string
	rows                       []fileRow
	changeRows                 []services.FileChange
	names                      []string
	indexed, limited           bool
	previewDiff, previewStaged bool
}

func (a *application) filesCount() int { return len(a.files.rows) }
func fileRowKey(row fileRow) string    { return fmt.Sprintf("%t:%s", row.staged, row.path) }
func (a *application) filesSelected() (fileRow, bool) {
	if a.list.Selected < 0 || a.list.Selected >= len(a.files.rows) {
		return fileRow{}, false
	}
	return a.files.rows[a.list.Selected], true
}
func (a *application) filesRebuild() {
	previous := ""
	if row, ok := a.filesSelected(); ok {
		previous = fileRowKey(row)
	}
	a.files.rows = nil
	query := strings.ToLower(a.files.query)
	if a.files.changes {
		for _, change := range a.files.changeRows {
			if query != "" && !strings.Contains(strings.ToLower(filepath.ToSlash(change.Path)), query) {
				continue
			}
			a.files.rows = append(a.files.rows, fileRow{path: filepath.Join(a.context.Root, change.Path), name: filepath.ToSlash(change.Path), status: change.Status, staged: change.Staged, deleted: change.Status == "D", untracked: change.Untracked})
		}
	} else if query != "" {
		for _, name := range a.files.names {
			if strings.Contains(strings.ToLower(filepath.ToSlash(name)), query) {
				a.files.rows = append(a.files.rows, fileRow{path: filepath.Join(a.context.Root, name), name: filepath.ToSlash(name)})
			}
		}
	} else if a.tree != nil {
		for _, row := range a.tree.rows {
			a.files.rows = append(a.files.rows, fileRow{path: row.path, name: row.name, depth: row.depth, directory: row.directory, symlink: row.symlink, expanded: row.expanded})
		}
	}
	for i, row := range a.files.rows {
		if fileRowKey(row) == previous {
			a.list.Selected = i
			break
		}
	}
	a.list.Clamp(len(a.files.rows), a.area().Height)
}
func (a *application) filesIndex(ctx context.Context) error {
	names, limited, err := services.FilenameFiles(ctx, a.context.Root)
	if err != nil {
		return err
	}
	a.files.names, a.files.limited, a.files.indexed = names, limited, true
	return nil
}
func (a *application) filesRefresh(ctx context.Context) {
	if a.preview != nil {
		a.filesPreview(ctx, a.previewPath, a.files.previewDiff, a.files.previewStaged)
		return
	}
	var err error
	if a.files.changes {
		var rows []services.FileChange
		rows, err = services.FileChanges(ctx, a.context.Root)
		if err == nil {
			a.files.changeRows = rows
		}
	} else {
		previousRows := a.tree.rows
		err = a.tree.refresh()
		if err != nil && len(previousRows) > 0 {
			a.tree.rows = previousRows
		}
		if err == nil && (a.files.query != "" || a.files.filtering) {
			err = a.filesIndex(ctx)
		}
	}
	if err != nil {
		a.message = err.Error()
		a.status.Fail(err)
	} else {
		a.message = ""
		a.status.Success()
		if a.files.limited && a.files.query != "" {
			a.message = "Filename index limited to 10,000 entries or two seconds."
		}
	}
	a.filesRebuild()
}
func (a *application) filesPreview(ctx context.Context, path string, diff, staged bool) {
	doc, err := services.ReadFileDocument(ctx, a.context.Root, path, diff, staged)
	if err != nil {
		a.message = err.Error()
		a.status.Fail(err)
		return
	}
	a.preview = safeFileLines(doc.Lines)
	a.previewPath = doc.Path
	a.previewOffset = 0
	a.files.previewDiff = diff
	a.files.previewStaged = staged
	a.message = ""
	a.status.Success()
}
func safeFileLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = safeText(strings.ReplaceAll(line, "\t", "    "))
	}
	return out
}

func (a *application) filesActivate(ctx context.Context, preview, diff, system bool) {
	row, ok := a.filesSelected()
	if !ok {
		a.message = "No file is selected."
		return
	}
	if row.directory {
		if !preview && !diff && !system {
			a.tree.expanded[row.path] = !row.expanded
			a.filesRefresh(ctx)
		} else {
			a.message = "Select a regular file."
		}
		return
	}
	if row.deleted || (a.files.changes && !row.untracked) {
		diff = true
	}
	path, err := services.ResolveFilePath(a.context.Root, row.path, diff)
	if err != nil {
		a.message = err.Error()
		return
	}
	if system {
		path, err = services.ResolveFilePath(a.context.Root, row.path, false)
		if err == nil {
			err = a.deps.openFile(ctx, path)
		}
		a.setError(err)
		return
	}
	if preview || a.callbacks.OpenFile == nil {
		a.filesPreview(ctx, path, diff, row.staged)
		return
	}
	if err = a.callbacks.OpenFile(model.FileRequest{Path: path, Diff: diff, Staged: row.staged}); err != nil {
		a.setError(err)
		return
	}
	a.message = "Opened " + filepath.Base(path) + " in the main work pane."
}

func (a *application) filesFilter(ctx context.Context, event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEscape:
		a.files.query = a.files.previousQuery
		a.files.filtering = false
	case tcell.KeyEnter:
		a.files.filtering = false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		runes := []rune(a.files.query)
		if len(runes) > 0 {
			a.files.query = string(runes[:len(runes)-1])
		}
	case tcell.KeyCtrlU:
		a.files.query = ""
	case tcell.KeyRune:
		if len([]rune(a.files.query)) < 256 {
			a.files.query += string(event.Rune())
		}
	}
	if !a.files.changes && !a.files.indexed {
		if err := a.filesIndex(ctx); err != nil {
			a.message = err.Error()
			a.status.Fail(err)
		}
	}
	a.list.Selected, a.list.Offset = 0, 0
	a.filesRebuild()
}
func (a *application) filesHandle(ctx context.Context, event tcell.Event) bool {
	if key, ok := event.(*tcell.EventKey); ok && a.files.filtering {
		a.filesFilter(ctx, key)
		return false
	}
	if a.preview != nil {
		if key, ok := event.(*tcell.EventKey); ok {
			if key.Key() == tcell.KeyEscape || key.Rune() == 'q' {
				a.preview = nil
				a.previewPath = ""
				return false
			}
			if key.Rune() == 'r' {
				a.filesRefresh(ctx)
				return false
			}
			if key.Rune() == 'o' {
				path, err := services.ResolveFilePath(a.context.Root, a.previewPath, false)
				if err == nil {
					err = a.deps.openFile(ctx, path)
				}
				a.setError(err)
				return false
			}
		}
		doc := ui.Document{Offset: a.previewOffset}
		doc.Handle(event, len(a.preview), a.area().Height)
		a.previewOffset = doc.Offset
		return false
	}
	if mouse, ok := event.(*tcell.EventMouse); ok {
		a.list.Handle(mouse, len(a.files.rows), a.area())
		return false
	}
	key, ok := event.(*tcell.EventKey)
	if !ok {
		return false
	}
	if key.Key() == tcell.KeyCtrlC {
		return true
	}
	if key.Key() == tcell.KeyEscape {
		if a.files.query != "" {
			a.files.query = ""
			a.filesRebuild()
			return false
		}
		return true
	}
	switch key.Rune() {
	case 'q':
		return true
	case '1', '2':
		a.files.changes = key.Rune() == '2'
		a.list.Selected, a.list.Offset = 0, 0
		a.filesRefresh(ctx)
		return false
	case '/':
		a.files.previousQuery = a.files.query
		a.files.filtering = true
		if !a.files.changes {
			if err := a.filesIndex(ctx); err != nil {
				a.message = err.Error()
				a.status.Fail(err)
			}
		}
		return false
	case 'r':
		a.files.indexed = false
		a.filesRefresh(ctx)
		return false
	case 'p':
		a.filesActivate(ctx, true, false, false)
		return false
	case 'd':
		a.filesActivate(ctx, false, true, false)
		return false
	case 'o':
		a.filesActivate(ctx, false, false, true)
		return false
	}
	if !a.files.changes && a.files.query == "" && (key.Key() == tcell.KeyRight || key.Key() == tcell.KeyLeft) {
		row, ok := a.filesSelected()
		if !ok {
			return false
		}
		if key.Key() == tcell.KeyRight && row.directory && !row.expanded {
			a.tree.expanded[row.path] = true
			a.filesRefresh(ctx)
		} else if key.Key() == tcell.KeyLeft {
			if row.expanded {
				delete(a.tree.expanded, row.path)
				a.filesRefresh(ctx)
			} else {
				parent := filepath.Dir(row.path)
				for i, candidate := range a.files.rows {
					if candidate.path == parent {
						a.list.Selected = i
						break
					}
				}
			}
		}
		return false
	}
	if handled, activate := a.list.Handle(event, len(a.files.rows), a.area()); handled && activate {
		a.filesActivate(ctx, false, false, false)
	}
	return false
}

func (a *application) filesDraw(last int) {
	if a.preview != nil {
		label := "Preview: " + a.previewPath
		if a.files.previewDiff {
			label = "Diff: " + a.previewPath
			if a.files.previewStaged {
				label = "Staged diff: " + a.previewPath
			}
		}
		a.write(contentStart-1, 0, label, normalStyle)
		doc := ui.Document{Offset: a.previewOffset}
		doc.Draw(a.canvas, ui.Rect{Y: contentStart, Width: a.area().Width, Height: max(0, last-contentStart)}, a.preview)
		a.previewOffset = doc.Offset
		return
	}
	mode := "1 Tree"
	if a.files.changes {
		mode = "2 Changes"
	}
	label := "[1 Tree] · 2 Changes · / filter"
	if a.files.changes {
		label = "1 Tree · [2 Changes] · / filter"
	}
	if a.files.query != "" || a.files.filtering {
		label = mode + " · Filter: " + a.files.query
		if a.files.filtering {
			label += "▏"
		}
	}
	a.write(contentStart-1, 0, label, normalStyle)
	if len(a.files.rows) == 0 {
		empty := "No matching files."
		if a.files.changes && a.files.query == "" {
			empty = "No Git changes."
			if a.status.Error != "" {
				empty = "Changes unavailable; Tree remains available."
			}
		}
		a.write(contentStart, 0, empty, normalStyle)
		return
	}
	a.list.Draw(a.canvas, ui.Rect{Y: contentStart, Width: a.area().Width, Height: max(0, last-contentStart)}, len(a.files.rows), func(i int) string {
		row := a.files.rows[i]
		if a.files.changes {
			section := "worktree"
			if row.staged {
				section = "staged"
			}
			if row.untracked {
				section = "untracked"
			}
			return row.status + " [" + section + "] " + row.name
		}
		prefix := "  "
		if row.directory {
			prefix = "▸ "
			if row.expanded {
				prefix = "▾ "
			}
		}
		if row.symlink {
			prefix = "↗ "
		}
		return strings.Repeat("  ", row.depth) + prefix + row.name
	})
}
func (a *application) filesHints() string {
	if a.files.filtering {
		return "Type filename · Enter apply · Esc cancel · Ctrl-U clear"
	}
	if a.preview != nil {
		return "? help · Esc/q back · ↑↓ scroll · r reload · o system"
	}
	return "Enter main · ? help · p preview · d diff · o system · / filter"
}
