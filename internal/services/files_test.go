package services

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fileGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-C", root}, args...)...)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
func fileRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	fileGit(t, root, "init", "--quiet", "--initial-branch=main")
	fileGit(t, root, "config", "user.name", "File Fixture")
	fileGit(t, root, "config", "user.email", "files@example.invalid")
	for _, name := range []string{"mixed.txt", "deleted.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("original\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fileGit(t, root, "add", ".")
	fileGit(t, root, "commit", "--quiet", "-m", "fixture")
	return root
}
func TestFileChangesSeparateStagedAndWorktreeAndPreserveLiteralPaths(t *testing.T) {
	root := fileRepo(t)
	path := filepath.Join(root, "mixed.txt")
	if err := os.WriteFile(path, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fileGit(t, root, "add", "mixed.txt")
	if err := os.WriteFile(path, []byte("worktree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	weird := "literal ;$(no execution)\nname.txt"
	if err := os.WriteFile(filepath.Join(root, weird), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := FileChanges(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	staged, worktree, deleted, untracked := false, false, false, false
	for _, row := range rows {
		switch row.Path {
		case "mixed.txt":
			if row.Staged {
				staged = true
			} else {
				worktree = true
			}
		case "deleted.txt":
			deleted = row.Status == "D"
		case weird:
			untracked = row.Untracked
		}
	}
	if !staged || !worktree || !deleted || !untracked {
		t.Fatal(rows)
	}
	stagedDoc, err := ReadFileDocument(context.Background(), root, "mixed.txt", true, true)
	if err != nil || !strings.Contains(strings.Join(stagedDoc.Lines, "\n"), "+staged") {
		t.Fatal(stagedDoc, err)
	}
	workDoc, err := ReadFileDocument(context.Background(), root, "mixed.txt", true, false)
	if err != nil || !strings.Contains(strings.Join(workDoc.Lines, "\n"), "+worktree") {
		t.Fatal(workDoc, err)
	}
	del, err := ReadFileDocument(context.Background(), root, "deleted.txt", true, false)
	if err != nil || !strings.Contains(strings.Join(del.Lines, "\n"), "-original") {
		t.Fatal(del, err)
	}
	if _, err = ReadFileDocument(context.Background(), root, "deleted.txt", false, false); err == nil {
		t.Fatal("deleted file was opened as content")
	}
}
func TestFileRenameStatusUsesNulBoundaries(t *testing.T) {
	rows, err := parseFileChanges("R  new name\n.txt\x00old name.txt\x00 M other.txt\x00")
	if err != nil || len(rows) != 2 || rows[0].Path != "new name\n.txt" || rows[0].OriginalPath != "old name.txt" || !rows[0].Staged {
		t.Fatal(rows, err)
	}
	if _, err = parseFileChanges("R  missing-source\x00"); err == nil {
		t.Fatal("accepted incomplete rename")
	}
}
func TestFileDiffUsesLiteralPathsAndDisablesExternalRenderers(t *testing.T) {
	root, rootErr := filepath.EvalSymlinks(t.TempDir())
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	path := "literal[glob];$(no).txt"
	if err := os.WriteFile(filepath.Join(root, path), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := run
	defer func() { run = previous }()
	run = func(_ context.Context, args []string, cwd string, _ time.Duration) (result, error) {
		joined := strings.Join(args, "|")
		for _, flag := range []string{"--literal-pathspecs", "--no-pager", "--no-ext-diff", "--no-textconv", "--color=never", "--cached"} {
			if !strings.Contains(joined, flag) {
				t.Fatal(args)
			}
		}
		if cwd != root || !reflect.DeepEqual(args[len(args)-2:], []string{"--", path}) {
			t.Fatal(args, cwd)
		}
		return result{stdout: "+ safe\n"}, nil
	}
	if _, err := ReadFileDocument(context.Background(), root, path, true, true); err != nil {
		t.Fatal(err)
	}
}
func TestFileDocumentConfinementBoundsAndDeletedParents(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	for _, tc := range []struct {
		path string
		diff bool
	}{{"../private", false}, {"escape/private", false}, {"escape/deleted", true}, {".", false}} {
		if _, err := ReadFileDocument(context.Background(), root, tc.path, tc.diff, false); err == nil {
			t.Fatalf("accepted escaped/special path: %+v", tc)
		}
	}
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("line\n", 30000)), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := ReadFileDocument(context.Background(), root, large, false, false)
	if err != nil || !doc.Truncated || len(doc.Lines) != FilePreviewLines+1 {
		t.Fatal(len(doc.Lines), doc.Truncated, err)
	}
	if err = os.WriteFile(large, []byte{'a', 0, 'b'}, 0600); err != nil {
		t.Fatal(err)
	}
	doc, err = ReadFileDocument(context.Background(), root, large, false, false)
	if err != nil || !doc.Binary {
		t.Fatal(doc, err)
	}
}
func TestFilenameIndexSkipsGitAndSymlinksAndHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src", ".git"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"README.md", "src/main.go", ".git/private"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Skip(err)
	}
	paths, limited, err := FilenameFiles(context.Background(), root)
	if err != nil || limited || !reflect.DeepEqual(paths, []string{"README.md", filepath.Join("src", "main.go")}) {
		t.Fatal(paths, limited, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, limited, err = FilenameFiles(ctx, root)
	if err != nil || !limited {
		t.Fatal(limited, err)
	}
}

func TestFileDirectoryLimitsBeforeSortingAndRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, limited, err := FileDirectory(root, root, 2)
	if err != nil || !limited || len(entries) != 2 {
		t.Fatal(entries, limited, err)
	}
	outside := t.TempDir()
	if _, _, err = FileDirectory(root, outside, 2); err == nil {
		t.Fatal("enumerated outside directory")
	}
}

func TestFileChangesScopeAndPathsFollowExplicitSubdirectoryRoot(t *testing.T) {
	root := fileRepo(t)
	sub := filepath.Join(root, "sub [literal]")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inside.txt"), []byte("inside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fileGit(t, root, "add", ".")
	fileGit(t, root, "commit", "--quiet", "-m", "subtree")
	if err := os.WriteFile(filepath.Join(sub, "inside.txt"), []byte("changed inside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mixed.txt"), []byte("changed outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "new name.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := FileChanges(context.Background(), sub)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		if row.Path != "inside.txt" && row.Path != "new name.txt" {
			t.Fatal("subdirectory scope leaked another change", rows)
		}
	}
	doc, err := ReadFileDocument(context.Background(), sub, "inside.txt", true, false)
	if err != nil || !strings.Contains(strings.Join(doc.Lines, "\n"), "+changed inside") {
		t.Fatal(doc, err)
	}
}

func TestFileChangesUseLiteralSubtreeAndRejectEscapedProviderPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err = os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	previous := run
	defer func() { run = previous }()
	calls := 0
	run = func(_ context.Context, args []string, _ string, _ time.Duration) (result, error) {
		calls++
		if calls == 1 {
			return result{stdout: root + "\n"}, nil
		}
		if !strings.Contains(strings.Join(args, "|"), "--literal-pathspecs") || !reflect.DeepEqual(args[len(args)-2:], []string{"--", "."}) {
			t.Fatal(args)
		}
		return result{stdout: " M outside.txt\x00"}, nil
	}
	if _, err = FileChanges(context.Background(), sub); err == nil || !strings.Contains(err.Error(), "outside the anchored") {
		t.Fatal(err)
	}
}
