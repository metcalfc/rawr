package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
)

func testApplication(t *testing.T, kind string, c model.Context, callbacks model.Callbacks) (*application, tcell.SimulationScreen) {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(100, 20)
	t.Cleanup(s.Fini)
	deps := dependencies{
		github:   func(context.Context, string, string) model.GitHubStatus { return model.GitHubStatus{State: "no-pr"} },
		vms:      func(context.Context) ([]model.VM, error) { return nil, nil },
		openURL:  func(context.Context, string) error { return nil },
		openFile: func(context.Context, string) error { return nil },
		ssh:      func(context.Context, model.VM) error { return nil },
	}
	if c.Root == "" {
		c.Root = t.TempDir()
	}
	a, err := newApplication(s, kind, c, callbacks, deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, s
}
func key(key tcell.Key) *tcell.EventKey { return tcell.NewEventKey(key, 0, tcell.ModNone) }
func character(r rune) *tcell.EventKey  { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }
func screenLine(s tcell.Screen, y int) string {
	width, _ := s.Size()
	var b strings.Builder
	for x := 0; x < width; x++ {
		r, _, _, _ := s.GetContent(x, y)
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

func TestVMBrowseDoesNotActivateAndFailureKeepsCurrentVM(t *testing.T) {
	activated := []string{}
	a, _ := testApplication(t, "exerawr", model.Context{VM: "old"}, model.Callbacks{SwitchVM: func(vm model.VM) error { activated = append(activated, vm.Name); return errors.New("cannot switch") }})
	a.vms = []model.VM{{Name: "old"}, {Name: "new"}}
	a.handle(context.Background(), key(tcell.KeyDown))
	if len(activated) != 0 || a.context.VM != "old" || a.list.Selected != 1 {
		t.Fatal("browsing changed current host")
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if len(activated) != 1 || activated[0] != "new" || a.context.VM != "old" || a.message != "cannot switch" {
		t.Fatalf("failed activation changed state: %+v %q", a.context, a.message)
	}
	a.callbacks.SwitchVM = func(vm model.VM) error { return nil }
	a.handle(context.Background(), character('s'))
	if a.context.VM != "new" {
		t.Fatal("successful activation did not select new host")
	}
}

func TestMouseUsesScrolledRowsWithoutConnecting(t *testing.T) {
	calls := []string{}
	a, s := testApplication(t, "exerawr", model.Context{}, model.Callbacks{SwitchVM: func(vm model.VM) error { calls = append(calls, vm.Name); return nil }})
	s.SetSize(60, 10)
	for i := 0; i < 20; i++ {
		a.vms = append(a.vms, model.VM{Name: fmt.Sprintf("vm-%02d", i)})
	}
	a.list.Selected = 18
	a.clamp()
	a.draw()
	i := a.list.Offset
	if i == 0 {
		t.Fatal("expected scrolled viewport")
	}
	press := tcell.NewEventMouse(4, contentStart, tcell.Button1, tcell.ModNone)
	a.handle(context.Background(), press)
	a.handle(context.Background(), press)
	a.handle(context.Background(), tcell.NewEventMouse(4, contentStart, tcell.ButtonNone, tcell.ModNone))
	if len(calls) != 0 || a.list.Selected != i {
		t.Fatalf("click did not safely select the visible VM: %v; row %d", calls, i)
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if len(calls) != 1 || calls[0] != a.vms[i].Name {
		t.Fatal("explicit connection used a different resource")
	}
	a.draw()
	if !strings.Contains(screenLine(s, 2), calls[0]) {
		t.Fatal("current VM header was not refreshed")
	}
}

func TestStandaloneSSHReturnsToUIAndKeepsHostOnFailure(t *testing.T) {
	a, _ := testApplication(t, "exerawr", model.Context{VM: "old"}, model.Callbacks{})
	a.vms = []model.VM{{Name: "new"}}
	called := false
	a.deps.ssh = func(_ context.Context, vm model.VM) error {
		called = true
		if vm.Name != "new" {
			t.Fatalf("wrong VM: %+v", vm)
		}
		return errors.New("ssh exited 255")
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if !called || a.context.VM != "old" || !strings.Contains(a.message, "255") {
		t.Fatal("SSH error not preserved")
	}
	a.deps.ssh = func(context.Context, model.VM) error { return nil }
	a.handle(context.Background(), key(tcell.KeyEnter))
	if a.context.VM != "new" {
		t.Fatal("successful standalone shell did not return selection")
	}
	a.draw() // A usable screen is retained after resume.
}

func TestBrowserUsesSelectedVMAndLinkFailuresPreserveContext(t *testing.T) {
	a, _ := testApplication(t, "exerawr", model.Context{VM: "old"}, model.Callbacks{LinkVM: func(model.VM) error { return errors.New("write failed") }})
	a.vms = []model.VM{{Name: "old"}, {Name: "new"}}
	a.list.Selected = 1
	opened := ""
	a.deps.openURL = func(_ context.Context, url string) error { opened = url; return nil }
	a.handle(context.Background(), character('b'))
	if opened != "https://new.exe.xyz" {
		t.Fatalf("browser did not use highlighted host: %s", opened)
	}
	a.handle(context.Background(), character('l'))
	if a.context.VM != "old" {
		t.Fatal("failed link replaced old host")
	}
	a.callbacks.LinkVM = func(model.VM) error { return nil }
	a.handle(context.Background(), character('l'))
	if a.context.VM != "new" {
		t.Fatal("successful link not reflected")
	}
}

func TestTreeAnchorsPreviewAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "child.txt"), []byte("child"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("hello\x1b[31m\tworld\nline\u202esecret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	a, s := testApplication(t, "filerawr", model.Context{Root: root}, model.Callbacks{})
	a.refresh(context.Background())
	if !a.tree.rows[0].directory || a.tree.rows[0].name != "dir" {
		t.Fatalf("directories not sorted first: %+v", a.tree.rows)
	}
	a.handle(context.Background(), key(tcell.KeyRight))
	if len(a.tree.rows) != 5 || a.tree.rows[1].name != "child.txt" {
		t.Fatalf("folder did not expand: %+v", a.tree.rows)
	}
	for i, row := range a.tree.rows {
		if row.name == "safe.txt" {
			a.list.Selected = i
		}
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if a.preview == nil || strings.ContainsAny(strings.Join(a.preview, "\n"), "\x1b\u202e") {
		t.Fatalf("unsafe preview: %q", a.preview)
	}
	a.draw()
	if !strings.Contains(screenLine(s, contentStart-1), "Preview:") || filepath.Base(a.previewPath) != "safe.txt" {
		t.Fatal("preview header missing selected file")
	}
	if a.handle(context.Background(), character('q')) || a.preview != nil {
		t.Fatal("q should return from preview")
	}
	for i, row := range a.tree.rows {
		if row.name == "external" {
			a.list.Selected = i
		}
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if a.preview != nil || !strings.Contains(a.message, "outside") {
		t.Fatal("preview escaped root")
	}
	for _, row := range a.tree.rows {
		if row.name == "loop" && (row.directory || row.expanded) {
			t.Fatal("followed directory symlink")
		}
	}
}

func TestPreviewBoundsBinaryAndUnicode(t *testing.T) {
	root := t.TempDir()
	tree, err := newTree(root)
	if err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("line\n", 30000)), 0600); err != nil {
		t.Fatal(err)
	}
	lines, err := tree.preview(large)
	if err != nil || len(lines) != 2001 || !strings.Contains(lines[len(lines)-1], "truncated") {
		t.Fatalf("preview not bounded: %d %v", len(lines), err)
	}
	if err := os.WriteFile(large, []byte{'A', 0, 'B'}, 0600); err != nil {
		t.Fatal(err)
	}
	lines, err = tree.preview(large)
	if err != nil || !strings.Contains(lines[0], "Binary") {
		t.Fatal("binary file was decoded as text")
	}
	if got := safeText("界 é 👾\x1b\u202e"); got != "界 é 👾��" {
		t.Fatalf("Unicode sanitized incorrectly: %q", got)
	}
}

func TestGitHubPRChooserAndInertScreen(t *testing.T) {
	a, s := testApplication(t, "ghrawr", model.Context{Repository: "owner/repo"}, model.Callbacks{})
	a.github = model.GitHubStatus{State: "ready", Repository: "owner/repo", Branch: "main", PRs: []model.PR{
		{Number: 1, Title: "first", URL: "https://github.com/owner/repo/pull/1"},
		{Number: 2, Title: "title\x1b[31m", URL: "https://github.com/owner/repo/pull/2", Checks: []model.Check{{Name: "build", Bucket: "pending", State: "QUEUED"}}},
	}}
	opened := ""
	a.deps.openURL = func(_ context.Context, u string) error { opened = u; return nil }
	a.handle(context.Background(), key(tcell.KeyDown))
	a.draw()
	if !strings.Contains(screenLine(s, contentStart+1), "title�[31m") {
		t.Fatal("service text was not inert")
	}
	a.handle(context.Background(), character('c'))
	a.draw()
	if !strings.Contains(screenLine(s, contentStart+2), "pending · build · QUEUED") {
		t.Fatal("selected PR checks missing")
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if opened != "https://github.com/owner/repo/pull/2" {
		t.Fatalf("opened wrong PR: %s", opened)
	}
}

func TestRefreshPreservesHighlightAndCurrentVM(t *testing.T) {
	a, _ := testApplication(t, "exerawr", model.Context{VM: "old"}, model.Callbacks{})
	a.vms = []model.VM{{Name: "old"}, {Name: "highlighted"}}
	a.list.Selected = 1
	a.deps.vms = func(context.Context) ([]model.VM, error) {
		return []model.VM{{Name: "another"}, {Name: "highlighted"}, {Name: "old"}}, nil
	}
	a.refresh(context.Background())
	awaitRefresh(t, a)
	if a.vms[a.list.Selected].Name != "highlighted" || a.context.VM != "old" {
		t.Fatal("refresh selected a new current host")
	}
	a.deps.vms = func(context.Context) ([]model.VM, error) { return nil, errors.New("offline") }
	a.refresh(context.Background())
	awaitRefresh(t, a)
	if len(a.vms) != 3 || a.context.VM != "old" || a.message != "offline" {
		t.Fatal("failed refresh discarded usable VM state")
	}
}

func TestGitHubChecksCanScrollInSmallPane(t *testing.T) {
	a, s := testApplication(t, "ghrawr", model.Context{}, model.Callbacks{})
	s.SetSize(60, 10)
	pr := model.PR{Number: 1, State: "OPEN", Title: "example"}
	for i := 0; i < 25; i++ {
		pr.Checks = append(pr.Checks, model.Check{Name: fmt.Sprintf("check-%02d", i), Bucket: "pass", State: "SUCCESS"})
	}
	a.github = model.GitHubStatus{State: "ready", PRs: []model.PR{pr}}
	a.handle(context.Background(), character('c'))
	a.handle(context.Background(), key(tcell.KeyPgDn))
	a.draw()
	if a.checkOffset <= 0 || !strings.Contains(screenLine(s, contentStart+2), fmt.Sprintf("check-%02d", a.checkOffset)) {
		t.Fatal("checks beyond the first screen are inaccessible")
	}
	if a.handle(context.Background(), character('q')) || a.checkDetails {
		t.Fatal("q should return from checks to PR chooser")
	}
}

func TestSimulationLoopQuitsAndContextCancelsSlowRefresh(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		a, s := testApplication(t, "exerawr", model.Context{}, model.Callbacks{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a.deps.vms = func(ctx context.Context) ([]model.VM, error) { <-ctx.Done(); return nil, ctx.Err() }
		done := make(chan error, 1)
		go func() { done <- a.loop(ctx) }()
		if cancelled {
			cancel()
		} else {
			_ = s.PostEvent(character('q'))
		}
		select {
		case err := <-done:
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel returned %v", err)
			}
			if !cancelled && err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("UI blocked on slow refresh")
		}
	}
}

func awaitRefresh(t *testing.T, a *application) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for a.loader.Loading() {
		a.drainResults()
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
