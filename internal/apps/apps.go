// Package apps implements independent resource panes. Rex owns their native
// surfaces; explicit actions are routed to the anchored work context.
package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

const contentStart = 5

type dependencies struct {
	github   func(context.Context, string, string) model.GitHubStatus
	checks   func(context.Context, string, string, model.PR) ([]model.Check, error)
	vms      func(context.Context) ([]model.VM, error)
	openURL  func(context.Context, string) error
	openFile func(context.Context, string) error
	ssh      func(context.Context, model.VM) error
}

func defaultDependencies() dependencies {
	return dependencies{github: services.GitHub, checks: services.GitHubChecks, vms: services.VMs, openURL: services.OpenURL, openFile: services.OpenFile, ssh: interactiveSSH}
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
func Run(ctx context.Context, kind string, c model.Context, callbacks model.Callbacks) error {
	a, err := newApplication(nil, kind, c, callbacks, defaultDependencies())
	if err != nil {
		return err
	}
	runErr := ui.Run(ctx, a)
	if message, err := a.githubFinalOutcome(); message != "" {
		fmt.Fprintln(os.Stdout, ui.SafeText(message))
		return errors.Join(runErr, err)
	}
	return runErr
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
	files         fileView
	gh            githubView
	github        model.GitHubStatus
	vms           []model.VM
	preview       []string
	previewPath   string
	previewOffset int
	checkDetails  bool
	checkOffset   int
	message       string
	status        ui.ResourceStatus
	help          bool
	document      ui.Document
	vmDetails     *model.VM
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
	a.githubClose()
}
func (a *application) loop(ctx context.Context) error { return ui.RunScreen(ctx, a.screen, a) }
func (a *application) refresh(ctx context.Context) {
	if a.tree != nil {
		a.filesRefresh(ctx)
		return
	}
	a.message = ""
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
	if err != nil {
		a.status.Fail(err)
		a.message = err.Error()
		return
	}
	a.message = ""
	if a.kind == "ghrawr" {
		a.githubApply(result.github)
		return
	}
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
	a.status.Success()
	a.clamp()
}
func (a *application) rowCount() int {
	if a.tree != nil {
		return a.filesCount()
	}
	if a.kind == "ghrawr" {
		return a.githubCount()
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
func (a *application) handle(ctx context.Context, event tcell.Event) bool {
	if _, ok := event.(*tcell.EventResize); ok {
		a.screen.Sync()
		a.clamp()
		return false
	}
	if e, ok := event.(*tcell.EventKey); ok {
		if e.Key() == tcell.KeyCtrlC {
			return true
		}
		if a.help {
			if e.Key() == tcell.KeyEscape || e.Rune() == 'q' || e.Rune() == '?' {
				a.help = false
				a.document.Offset = 0
				return false
			}
		} else if e.Rune() == '?' && !a.files.filtering && !a.gh.filtering {
			a.help = true
			a.document.Offset = 0
			return false
		}
	}
	if a.help {
		a.document.Handle(event, len(a.helpLines()), a.area().Height)
		return false
	}
	switch a.kind {
	case "ghrawr":
		return a.githubHandle(ctx, event)
	case "filerawr":
		return a.filesHandle(ctx, event)
	default:
		return a.vmHandle(ctx, event)
	}
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
	title := a.kind
	if a.context.Name != "" {
		title += " · " + a.context.Name
	}
	a.write(0, 0, title, titleStyle)
	rootLabel := a.context.Root
	if width < 70 {
		rootLabel = filepath.Base(rootLabel) + " (anchored)"
	}
	a.write(1, 0, "Root: "+rootLabel, normalStyle)
	contextLine := "Files · Enter opens in the center"
	switch a.kind {
	case "ghrawr":
		repo := a.github.Repository
		if repo == "" {
			repo = a.context.Repository
		}
		contextLine = repo + " · " + a.github.Branch
	case "exerawr":
		vm := a.context.VM
		if vm == "" {
			vm = "(none connected)"
		}
		contextLine = "VM context: " + vm
	}
	a.write(2, 0, contextLine, normalStyle)
	a.write(3, 0, a.status.Line(a.loader.Loading()), normalStyle)
	last := max(contentStart, height-2)
	hints := "↑↓ browse · r refresh · ? help · q quit"
	if a.help {
		a.document.Draw(a.canvas, a.area(), a.helpLines())
		hints = "↑↓/PgUp/PgDn scroll · ?/Esc/q back"
	} else {
		switch a.kind {
		case "ghrawr":
			a.githubDraw(last)
			hints = a.githubHints()
		case "filerawr":
			a.filesDraw(last)
			hints = a.filesHints()
		default:
			a.vmDraw(last)
			hints = a.vmHints()
		}
	}
	a.write(height-2, 0, a.message, titleStyle)
	if height < 8 {
		hints = "Grow pane to 8 rows · ? help · q quit"
	}
	a.write(height-1, 0, hints, normalStyle)
}
func (a *application) helpLines() []string {
	lines := []string{"A rawr browses resources; explicit actions drive your work.", ""}
	if a.message != "" {
		lines = append(lines, "Last result: "+a.message, "")
	}
	if a.status.Error != "" && a.status.Error != a.message {
		lines = append(lines, "Refresh error: "+a.status.Error, "")
	}
	lines = append(lines, "↑/↓ or j/k    Browse without opening or changing work", "Home/End      First/last row", "PgUp/PgDn     Scroll", "r             Refresh (last good rows survive errors)", "?             Show/close this help", "Esc / q       Back from details, otherwise quit this app", "", "Rex owns pane focus and layout. Use your Rex keybindings.", "--hide / --toggle retain the running app; q exits it.", "The project root remains anchored when focus changes.", "")
	switch a.kind {
	case "ghrawr":
		lines = append(lines, "1 / 2         Pull requests / issues", "/             Filter the visible resource list", "Enter / o     Open selected PR or issue in your browser", "c             Inspect checks for selected PR", "x             Checkout selected PR after y/n confirmation", "Checkout requires the matching clean local project.", "In Rex, return to idle original local work first.")
	case "filerawr":
		lines = append(lines, "1 / 2         Tree / Git changes", "/             Filter file names", "← / →         Collapse / expand directories", "Enter         Expand directory or open file/diff in center", "p             Preview here", "d             Open diff", "o             Open with the system application", "Outside Rex, Enter previews here.")
	case "exerawr":
		lines = append(lines, "Enter / s     Connect selected VM in the center", "i             Inspect selected VM", "b             Open selected VM in your browser", "l             Link selected VM when project linking is available", "Mouse click   Select only; Enter or s connects", "Previous local work and SSH shells remain in native tabs.", "exerawr --local brings original local work back.")
	}
	lines = append(lines, "", "Anchored root: "+a.context.Root)
	return ui.WrapLines(lines, a.area().Width)
}
