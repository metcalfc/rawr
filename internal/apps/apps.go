// Package apps contains the terminal interfaces shared by the three standalone commands.
// Pane placement and deliberate saved setups are owned by the calling Rex/Rawr layer.
package apps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

const contentStart = 4

type dependencies struct {
	github   func(context.Context, string, string) model.GitHubStatus
	vms      func(context.Context) ([]model.VM, error)
	openURL  func(context.Context, string) error
	openFile func(context.Context, string) error
	ssh      func(context.Context, model.VM) error
}

func defaultDependencies() dependencies {
	return dependencies{github: services.GitHub, vms: services.VMs, openURL: services.OpenURL, openFile: services.OpenFile, ssh: interactiveSSH}
}
func interactiveSSH(ctx context.Context, vm model.VM) error {
	argv, err := services.SSHArgs(vm)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Run starts a full terminal UI. Outside Rex, exerawr temporarily returns its own
// TTY to ssh, then resumes the selector when the remote shell exits.
func Run(ctx context.Context, kind string, c model.Context, callbacks model.Callbacks) error {
	a, err := newApplication(nil, kind, c, callbacks, defaultDependencies())
	if err != nil {
		return err
	}
	return ui.Run(ctx, a)
}

type appData struct {
	github model.GitHubStatus
	vms    []model.VM
}
type application struct {
	screen        tcell.Screen
	canvas        *ui.Canvas
	list          ui.List
	loader        *ui.Refresh[appData]
	kind          string
	context       model.Context
	callbacks     model.Callbacks
	deps          dependencies
	tree          *tree
	github        model.GitHubStatus
	vms           []model.VM
	preview       []string
	previewPath   string
	previewOffset int
	checkDetails  bool
	checkOffset   int
	message       string
}

func newApplication(screen tcell.Screen, kind string, c model.Context, callbacks model.Callbacks, deps dependencies) (*application, error) {
	switch kind {
	case "ghrawr", "filerawr", "exerawr":
	default:
		return nil, fmt.Errorf("unknown app %q", kind)
	}
	a := &application{screen: screen, kind: kind, context: c, callbacks: callbacks, deps: deps}
	if kind == "filerawr" {
		t, err := newTree(c.Root)
		if err != nil {
			return nil, fmt.Errorf("cannot open project tree: %w", err)
		}
		a.tree = t
		a.context.Root = t.root
	}
	if screen != nil {
		if err := a.Init(ui.NewCanvas(screen)); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (a *application) Init(canvas *ui.Canvas) error {
	a.canvas, a.screen = canvas, canvas.Screen
	if a.loader != nil {
		a.loader.Close()
	}
	a.loader = ui.NewRefresh[appData](canvas.Wake)
	return nil
}
func (a *application) Refresh(ctx context.Context) { a.refresh(ctx) }
func (a *application) Handle(ctx context.Context, event tcell.Event) bool {
	return a.handle(ctx, event)
}
func (a *application) Draw(_ *ui.Canvas) { a.drainResults(); a.render() }
func (a *application) Close() {
	if a.loader != nil {
		a.loader.Close()
	}
}
func (a *application) loop(ctx context.Context) error { return ui.RunScreen(ctx, a.screen, a) }

func (a *application) refresh(ctx context.Context) {
	if a.tree != nil {
		path := ""
		if len(a.tree.rows) > a.list.Selected {
			path = a.tree.rows[a.list.Selected].path
		}
		a.message = ""
		if err := a.tree.refresh(); err != nil {
			a.message = err.Error()
		}
		for i, row := range a.tree.rows {
			if row.path == path {
				a.list.Selected = i
				break
			}
		}
		a.clamp()
		return
	}
	a.message = "Refreshing…"
	kind, root, repository, deps := a.kind, a.context.Root, a.context.Repository, a.deps
	a.loader.Start(ctx, func(ctx context.Context) (appData, error) {
		if kind == "ghrawr" {
			return appData{github: deps.github(ctx, root, repository)}, nil
		}
		vms, err := deps.vms(ctx)
		return appData{vms: vms}, err
	})
}

func (a *application) drainResults() {
	result, err, ready := a.loader.Poll()
	if !ready {
		return
	}
	a.message = ""
	if err != nil {
		a.message = err.Error()
		return
	}
	if a.kind == "ghrawr" {
		number := 0
		if len(a.github.PRs) > a.list.Selected {
			number = a.github.PRs[a.list.Selected].Number
		}
		a.github = result.github
		for i, pr := range a.github.PRs {
			if pr.Number == number {
				a.list.Selected = i
				break
			}
		}
		if len(a.github.PRs) > a.list.Selected {
			a.checkOffset = min(a.checkOffset, max(0, len(a.github.PRs[a.list.Selected].Checks)-1))
		}
	} else {
		previous := a.context.VM
		if len(a.vms) > a.list.Selected {
			previous = a.vms[a.list.Selected].Name
		}
		a.vms = result.vms
		a.list.Selected = 0
		for i, vm := range a.vms {
			if vm.Name == previous {
				a.list.Selected = i
				break
			}
		}
	}
	a.clamp()
}

func (a *application) rowCount() int {
	if a.tree != nil {
		return len(a.tree.rows)
	}
	if a.kind == "ghrawr" {
		return len(a.github.PRs)
	}
	return len(a.vms)
}
func (a *application) area() ui.Rect {
	width, height := a.screen.Size()
	return ui.Rect{Y: contentStart, Width: width, Height: max(0, height-contentStart-2)}
}
func (a *application) clamp() { a.list.Clamp(a.rowCount(), a.area().Height) }
func (a *application) setError(err error) {
	if err != nil {
		a.message = err.Error()
	} else {
		a.message = ""
	}
}

func (a *application) activateVM(ctx context.Context) {
	if len(a.vms) <= a.list.Selected {
		a.message = "No existing VM is available to open."
		return
	}
	vm := a.vms[a.list.Selected]
	if _, err := services.SSHArgs(vm); err != nil {
		a.setError(err)
		return
	}
	var err error
	if a.callbacks.SwitchVM != nil {
		err = a.callbacks.SwitchVM(vm)
	} else {
		err = a.canvas.Suspend(func() error { return a.deps.ssh(ctx, vm) })
	}
	if err != nil {
		a.setError(err)
		return
	}
	a.context.VM = vm.Name
	a.message = "Opened " + vm.Name
}
func (a *application) linkVM() {
	if len(a.vms) <= a.list.Selected {
		a.message = "No existing VM is available to link."
		return
	}
	if a.callbacks.LinkVM == nil {
		a.message = "Use rawr link PROJECT VM to save a project link."
		return
	}
	vm := a.vms[a.list.Selected]
	if err := a.callbacks.LinkVM(vm); err != nil {
		a.setError(err)
		return
	}
	a.context.VM = vm.Name
	a.message = "Linked " + vm.Name
}
func (a *application) openLinkedVM(ctx context.Context) {
	for _, vm := range a.vms {
		if vm.Name == a.context.VM {
			u, err := services.BrowserURL(vm)
			if err == nil {
				err = a.deps.openURL(ctx, u)
			}
			a.setError(err)
			return
		}
	}
	a.message = "The current VM is unavailable. Refresh and select an existing VM."
}
func (a *application) openPR(ctx context.Context) {
	if len(a.github.PRs) <= a.list.Selected {
		a.message = "There is no pull request to open."
		return
	}
	a.setError(a.deps.openURL(ctx, a.github.PRs[a.list.Selected].URL))
}
func (a *application) openTree(ctx context.Context, system bool) {
	if len(a.tree.rows) <= a.list.Selected {
		return
	}
	row := a.tree.rows[a.list.Selected]
	if row.directory {
		a.tree.expanded[row.path] = !row.expanded
		a.refresh(ctx)
		return
	}
	path, err := a.tree.inside(row.path)
	if err != nil {
		a.setError(err)
		return
	}
	if system {
		info, err := os.Stat(path)
		if err != nil {
			a.setError(err)
			return
		}
		if !info.Mode().IsRegular() {
			a.message = "Select a regular file to open."
			return
		}
		a.setError(a.deps.openFile(ctx, path))
		return
	}
	preview, err := a.tree.preview(path)
	if err != nil {
		a.setError(err)
		return
	}
	a.preview, a.previewPath, a.previewOffset, a.message = preview, path, 0, ""
}

func (a *application) handle(ctx context.Context, event tcell.Event) bool {
	switch event := event.(type) {
	case *tcell.EventResize:
		a.screen.Sync()
		a.clamp()
	case *tcell.EventMouse:
		buttons := event.Buttons()
		if a.checkDetails {
			count := 0
			if len(a.github.PRs) > a.list.Selected {
				count = len(a.github.PRs[a.list.Selected].Checks)
			}
			if buttons&tcell.WheelUp != 0 {
				a.checkOffset = max(0, a.checkOffset-1)
			}
			if buttons&tcell.WheelDown != 0 {
				a.checkOffset = min(max(0, count-1), a.checkOffset+1)
			}
			return false
		}
		if a.preview != nil {
			return false
		}
		_, activate := a.list.Handle(event, a.rowCount(), a.area())
		if activate && a.kind == "exerawr" {
			a.activateVM(ctx)
		}

	case *tcell.EventKey:
		if event.Key() == tcell.KeyCtrlC {
			return true
		}
		if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
			if a.preview != nil {
				a.preview = nil
				a.previewPath = ""
				return false
			}
			if a.checkDetails {
				a.checkDetails = false
				return false
			}
			return true
		}
		if a.checkDetails {
			count := 0
			if len(a.github.PRs) > a.list.Selected {
				count = len(a.github.PRs[a.list.Selected].Checks)
			}
			switch {
			case event.Key() == tcell.KeyUp || event.Rune() == 'k':
				a.checkOffset = max(0, a.checkOffset-1)
			case event.Key() == tcell.KeyDown || event.Rune() == 'j':
				a.checkOffset = min(max(0, count-1), a.checkOffset+1)
			case event.Key() == tcell.KeyPgDn:
				a.checkOffset = min(max(0, count-1), a.checkOffset+10)
			case event.Key() == tcell.KeyPgUp:
				a.checkOffset = max(0, a.checkOffset-10)
			case event.Rune() == 'c':
				a.checkDetails = false
			case event.Rune() == 'r':
				a.refresh(ctx)
			case event.Key() == tcell.KeyEnter || event.Rune() == 'o':
				a.openPR(ctx)
			}
			return false
		}
		if a.preview != nil {
			switch {
			case event.Key() == tcell.KeyUp || event.Rune() == 'k':
				a.previewOffset = max(0, a.previewOffset-1)
			case event.Key() == tcell.KeyDown || event.Rune() == 'j':
				a.previewOffset = min(max(0, len(a.preview)-1), a.previewOffset+1)
			case event.Key() == tcell.KeyPgDn:
				a.previewOffset = min(max(0, len(a.preview)-1), a.previewOffset+10)
			case event.Key() == tcell.KeyPgUp:
				a.previewOffset = max(0, a.previewOffset-10)
			case event.Rune() == 'o':
				a.setError(a.deps.openFile(ctx, a.previewPath))
			}
			return false
		}
		if handled, activate := a.list.Handle(event, a.rowCount(), a.area()); handled {
			if activate {
				switch a.kind {
				case "exerawr":
					a.activateVM(ctx)
				case "ghrawr":
					a.openPR(ctx)
				case "filerawr":
					a.openTree(ctx, false)
				}
			}
			return false
		}
		switch {
		case event.Rune() == 'r':
			a.refresh(ctx)
		case a.tree != nil && event.Key() == tcell.KeyRight:
			if len(a.tree.rows) > a.list.Selected {
				row := a.tree.rows[a.list.Selected]
				if row.directory && !row.expanded {
					a.tree.expanded[row.path] = true
					a.refresh(ctx)
				}
			}
		case a.tree != nil && event.Key() == tcell.KeyLeft:
			if len(a.tree.rows) > a.list.Selected {
				row := a.tree.rows[a.list.Selected]
				if row.expanded {
					delete(a.tree.expanded, row.path)
					a.refresh(ctx)
				} else {
					parent := filepath.Dir(row.path)
					for i, candidate := range a.tree.rows {
						if candidate.path == parent {
							a.list.Selected = i
							break
						}
					}
				}
			}
		case a.kind == "exerawr" && (event.Key() == tcell.KeyEnter || event.Rune() == 's'):
			a.activateVM(ctx)
		case a.kind == "exerawr" && event.Rune() == 'l':
			a.linkVM()
		case a.kind == "exerawr" && event.Rune() == 'b':
			a.openLinkedVM(ctx)
		case a.kind == "ghrawr" && (event.Key() == tcell.KeyEnter || event.Rune() == 'o'):
			a.openPR(ctx)
		case a.kind == "ghrawr" && event.Rune() == 'c':
			if len(a.github.PRs) > a.list.Selected {
				a.checkDetails = true
				a.checkOffset = 0
			}
		case a.tree != nil && (event.Key() == tcell.KeyEnter || event.Rune() == 'o'):
			a.openTree(ctx, event.Rune() == 'o')
		}
		a.clamp()
	}
	return false
}

var normalStyle = ui.DefaultTheme().Normal
var titleStyle = ui.DefaultTheme().Title

func (a *application) write(y, x int, value string, style tcell.Style) {
	a.canvas.Text(x, y, value, style)
}

func (a *application) draw() {
	a.screen.SetStyle(normalStyle)
	a.screen.Clear()
	a.render()
	a.screen.Show()
}
func (a *application) render() {
	width, height := a.screen.Size()
	_ = width
	title := a.kind
	if a.context.Name != "" {
		title += " · " + a.context.Name
	}
	a.write(0, 0, title, titleStyle)
	a.write(1, 0, "Root: "+a.context.Root, normalStyle)
	context := ""
	if a.kind == "ghrawr" {
		repo := a.github.Repository
		if repo == "" {
			repo = a.context.Repository
		}
		context = "Repository: " + repo + " · Branch: " + a.github.Branch
	} else if a.kind == "exerawr" {
		vm := a.context.VM
		if vm == "" {
			vm = "(none selected)"
		}
		context = "Current VM: " + vm
	} else if a.preview != nil {
		context = "Preview: " + a.previewPath
	} else {
		context = "Files stay anchored to this root."
	}
	a.write(2, 0, context, normalStyle)
	last := max(contentStart, height-2)
	if a.preview != nil {
		for y, i := contentStart, a.previewOffset; y < last && i < len(a.preview); y, i = y+1, i+1 {
			a.write(y, 0, a.preview[i], normalStyle)
		}
	} else if a.checkDetails {
		if len(a.github.PRs) > a.list.Selected {
			pr := a.github.PRs[a.list.Selected]
			a.write(contentStart, 0, fmt.Sprintf("#%d [%s] %s", pr.Number, pr.State, pr.Title), titleStyle)
			if pr.ChecksError != "" {
				a.write(contentStart+1, 0, pr.ChecksError, normalStyle)
			} else if len(pr.Checks) == 0 {
				a.write(contentStart+1, 0, "No checks reported for this pull request.", normalStyle)
			}
			for y, i := contentStart+2, a.checkOffset; y < last && i < len(pr.Checks); y, i = y+1, i+1 {
				check := pr.Checks[i]
				a.write(y, 0, check.Bucket+" · "+check.Name+" · "+check.State, normalStyle)
			}
		} else {
			a.write(contentStart, 0, a.github.Message, normalStyle)
		}
	} else if a.tree != nil {
		a.list.Draw(a.canvas, a.area(), len(a.tree.rows), func(i int) string {
			row := a.tree.rows[i]
			prefix := "  "
			if row.directory {
				prefix = "▸ "
				if row.expanded {
					prefix = "▾ "
				}
			}
			name := row.name
			if row.symlink {
				name += " ↗"
			}
			return strings.Repeat("  ", min(row.depth, 100)) + prefix + name
		})
		if len(a.tree.rows) == 0 {
			a.write(contentStart, 0, "(empty directory)", normalStyle)
		}
	} else if a.kind == "exerawr" {
		a.list.Draw(a.canvas, a.area(), len(a.vms), func(i int) string {
			vm := a.vms[i]
			marker := "  "
			if vm.Name == a.context.VM {
				marker = "● "
			}
			return marker + vm.Name + "  " + vm.Status
		})
		if len(a.vms) == 0 && !a.loader.Loading() {
			a.write(contentStart, 0, "No existing VMs found. Press r to refresh.", normalStyle)
		}
	} else {
		y := a.list.Draw(a.canvas, a.area(), len(a.github.PRs), func(i int) string {
			pr := a.github.PRs[i]
			return fmt.Sprintf("#%d [%s] %s", pr.Number, pr.State, pr.Title)
		})
		if len(a.github.PRs) > a.list.Selected && y+1 < last {
			pr := a.github.PRs[a.list.Selected]
			y++
			if pr.ChecksError != "" {
				a.write(y, 0, pr.ChecksError, normalStyle)
				y++
			}
			for _, check := range pr.Checks {
				if y >= last {
					break
				}
				a.write(y, 0, check.Bucket+" · "+check.Name+" · "+check.State, normalStyle)
				y++
			}
		}
		if a.github.Message != "" {
			a.write(contentStart, 0, a.github.Message, normalStyle)
		}
	}
	a.write(height-2, 0, a.message, titleStyle)
	hints := "↑↓ browse · r refresh · q quit"
	switch a.kind {
	case "filerawr":
		hints = "↑↓ browse · ←→ folders · Enter preview · o open · r refresh · q back/quit"
	case "ghrawr":
		hints = "↑↓ PRs · c checks · Enter/o open · r refresh · q quit"
		if a.checkDetails {
			hints = "↑↓/PgUp/PgDn scroll checks · o open PR · r refresh · c/q back"
		}
	case "exerawr":
		hints = "↑↓ browse · Enter/s/click shell · b browser · r refresh · q quit"
		if a.callbacks.LinkVM != nil {
			hints = "↑↓ browse · Enter/s/click shell · l link · b browser · r refresh · q quit"
		}
	}
	if height < 7 {
		hints = "Grow pane to 7 rows · q quit"
	}
	a.write(height-1, 0, hints, normalStyle)
}
