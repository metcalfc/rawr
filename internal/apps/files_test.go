package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

func appFileGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func selectFileRow(t *testing.T, a *application, name string, staged bool) {
	t.Helper()
	for i, row := range a.files.rows {
		if row.name == name && row.staged == staged {
			a.list.Selected = i
			return
		}
	}
	t.Fatalf("no row %s staged=%t: %+v", name, staged, a.files.rows)
}

func TestFilesBrowseMouseAndPreviewNeverOpenMainUntilEnter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("hello\x1b[31m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opened := []model.FileRequest{}
	a, s := testApplication(t, "filerawr", model.Context{Root: root}, model.Callbacks{OpenFile: func(request model.FileRequest) error { opened = append(opened, request); return nil }})
	a.filesRefresh(context.Background())
	a.filesHandle(context.Background(), tcell.NewEventMouse(1, contentStart, tcell.Button1, tcell.ModNone))
	a.filesHandle(context.Background(), tcell.NewEventMouse(1, contentStart, tcell.ButtonNone, tcell.ModNone))
	a.filesHandle(context.Background(), key(tcell.KeyDown))
	if len(opened) != 0 {
		t.Fatal("browsing activated a file")
	}
	a.filesHandle(context.Background(), character('p'))
	if a.preview == nil || len(opened) != 0 || strings.Contains(strings.Join(a.preview, "\n"), "\x1b") {
		t.Fatal(a.preview, opened)
	}
	a.draw()
	if !strings.Contains(screenLine(s, contentStart-1), "Preview:") {
		t.Fatal(screenLine(s, contentStart-1))
	}
	a.filesHandle(context.Background(), character('q'))
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if len(opened) != 1 || filepath.Base(opened[0].Path) != "one.txt" || opened[0].Diff {
		t.Fatal(opened)
	}
	a.filesHandle(context.Background(), character('d'))
	if len(opened) != 2 || !opened[1].Diff {
		t.Fatal(opened)
	}
}

func TestFilesTreeExpansionAndFilenameFilterAreAnchored(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "MixedName.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := testApplication(t, "filerawr", model.Context{Root: root}, model.Callbacks{})
	a.filesRefresh(context.Background())
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if len(a.files.rows) != 2 || !a.files.rows[0].expanded {
		t.Fatal(a.files.rows)
	}
	a.filesHandle(context.Background(), character('/'))
	for _, r := range "mixedname" {
		a.filesHandle(context.Background(), character(r))
	}
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if len(a.files.rows) != 1 || a.files.rows[0].name != "src/MixedName.go" {
		t.Fatal(a.files.rows)
	}
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if a.preview == nil || filepath.Base(a.previewPath) != "MixedName.go" {
		t.Fatal(a.preview, a.previewPath)
	}
	a.filesHandle(context.Background(), key(tcell.KeyEscape))
	a.filesHandle(context.Background(), key(tcell.KeyEscape))
	if a.files.query != "" || len(a.files.rows) != 2 {
		t.Fatal(a.files)
	}
}

func TestFilesChangesDistinctDiffActionsAndStaleRows(t *testing.T) {
	root := t.TempDir()
	appFileGit(t, root, "init", "--quiet", "--initial-branch=main")
	appFileGit(t, root, "config", "user.name", "Fixture")
	appFileGit(t, root, "config", "user.email", "fixture@example.invalid")
	path := filepath.Join(root, "mixed.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appFileGit(t, root, "add", ".")
	appFileGit(t, root, "commit", "--quiet", "-m", "fixture")
	if err := os.WriteFile(path, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	appFileGit(t, root, "add", "mixed.txt")
	if err := os.WriteFile(path, []byte("unstaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := []model.FileRequest{}
	a, _ := testApplication(t, "filerawr", model.Context{Root: root}, model.Callbacks{OpenFile: func(request model.FileRequest) error { requests = append(requests, request); return nil }})
	a.filesHandle(context.Background(), character('2'))
	if len(a.files.rows) != 2 {
		t.Fatal(a.files.rows)
	}
	selectFileRow(t, a, "mixed.txt", true)
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	selectFileRow(t, a, "mixed.txt", false)
	a.filesHandle(context.Background(), character('d'))
	if len(requests) != 2 || !requests[0].Diff || !requests[0].Staged || !requests[1].Diff || requests[1].Staged {
		t.Fatal(requests)
	}
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "git-away")); err != nil {
		t.Fatal(err)
	}
	a.filesRefresh(context.Background())
	if len(a.files.rows) != 2 || a.status.Error == "" || a.status.Updated.IsZero() {
		t.Fatal(a.files.rows, a.status)
	}
}

func TestFilesDeletedDiffAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	appFileGit(t, root, "init", "--quiet", "--initial-branch=main")
	appFileGit(t, root, "config", "user.name", "Fixture")
	appFileGit(t, root, "config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "deleted.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	appFileGit(t, root, "add", ".")
	appFileGit(t, root, "commit", "--quiet", "-m", "fixture")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	requests := []model.FileRequest{}
	a, _ := testApplication(t, "filerawr", model.Context{Root: root}, model.Callbacks{OpenFile: func(request model.FileRequest) error { requests = append(requests, request); return nil }})
	a.filesHandle(context.Background(), character('2'))
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if len(requests) != 1 || !requests[0].Diff {
		t.Fatal(requests)
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	a.filesHandle(context.Background(), character('1'))
	selectFileRow(t, a, "escape", false)
	a.filesHandle(context.Background(), key(tcell.KeyEnter))
	if len(requests) != 1 || !strings.Contains(a.message, "outside") {
		t.Fatal(requests, a.message)
	}
}

func TestReadOnlyFileViewReloadScrollAndInertContents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "readme.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\x1b[31m\u202e\n", 80)), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := newFileDocumentView(context.Background(), root, path, false, false)
	if err != nil {
		t.Fatal(err)
	}
	s := tcell.NewSimulationScreen("UTF-8")
	if err = s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(60, 12)
	canvas := ui.NewCanvas(s)
	if err = v.Init(canvas); err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	v.Refresh(context.Background())
	awaitFileView(t, v)
	v.Handle(context.Background(), key(tcell.KeyEnd))
	if v.document.Offset == 0 {
		t.Fatal("viewer did not scroll")
	}
	v.Draw(canvas)
	if strings.ContainsAny(strings.Join(v.lines, "\n"), "\x1b\u202e") {
		t.Fatal("active controls in viewer")
	}
	if err = os.WriteFile(path, []byte("updated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v.Handle(context.Background(), character('r'))
	awaitFileView(t, v)
	if v.lines[0] != "updated" {
		t.Fatal(v.lines)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	v.Handle(context.Background(), character('r'))
	awaitFileView(t, v)
	if v.lines[0] != "updated" || v.status.Error == "" {
		t.Fatal("reload discarded last good content", v.lines, v.status)
	}
	if !v.Handle(context.Background(), character('q')) {
		t.Fatal("q did not quit viewer")
	}
	outside, err := newFileDocumentView(context.Background(), root, filepath.Join(t.TempDir(), "outside"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer outside.Close()
	if err = outside.Init(canvas); err != nil {
		t.Fatal(err)
	}
	outside.Refresh(context.Background())
	awaitFileView(t, outside)
	if outside.status.Error == "" || len(outside.lines) != 0 {
		t.Fatal("viewer read an outside path", outside.lines, outside.status)
	}
}
