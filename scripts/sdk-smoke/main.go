// sdk-smoke builds a real external module against the public rawrkit packages.
// The resulting program constructs a custom app and UI view without connecting
// to Rex, opening a terminal, reading Rawr projects, or launching a subprocess.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const consumer = `package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/gdamore/tcell/v2"
    "github.com/metcalfc/rawr/rawrkit"
    "github.com/metcalfc/rawr/rawrkit/rex"
    "github.com/metcalfc/rawr/rawrkit/ui"
)

type notesView struct { list ui.List; activated bool }
func (*notesView) Init(*ui.Canvas) error { return nil }
func (*notesView) Refresh(context.Context) {}
func (v *notesView) Handle(_ context.Context, event tcell.Event) bool {
    _, activate := v.list.Handle(event, 2, ui.Rect{X:0, Y:1, Width:40, Height:4})
    v.activated = v.activated || activate
    return activate
}
func (v *notesView) Draw(c *ui.Canvas) {
    c.Text(0, 0, "SDK notes", c.Theme.Title)
    v.list.Draw(c, ui.Rect{X:0, Y:1, Width:40, Height:4}, 2, func(index int) string {
        return []string{"First note", "Second note"}[index]
    })
}
var _ ui.View = (*notesView)(nil)

func main() {
    root, err := os.Getwd()
    if err != nil { panic(err) }
    client := rex.New("rex", "", "")
    manager := rawrkit.New(client, filepath.Join(root, "unused-state"), root,
        rawrkit.Definition{
            ID: "sdk-notes", Title: "SDK Notes",
            Aliases: []string{"my-notes"},
            Command: []string{"printf", "rawrkit SDK"},
            Placement: rawrkit.PlacementSpec{Direction: "horizontal", Side: "after", Ratio: .25},
        })
    canonical, err := manager.Canonical("my-notes")
    if err != nil || canonical != "sdk-notes" { panic("custom SDK registration/alias failed") }
    anchored := rawrkit.Context{Root: root, Scope: "external-sdk", Session: "not-connected", Anchor: "not-connected"}
    contextPath := filepath.Join(root, "native-context.json")
    encoded, err := json.Marshal(anchored)
    if err != nil { panic(err) }
    if err := os.WriteFile(contextPath, encoded, 0600); err != nil { panic(err) }
    decoded, err := rawrkit.ReadContext(contextPath)
    if err != nil || decoded.Root != root || decoded.Scope != anchored.Scope {
        panic("public SDK context round trip failed")
    }
    // Type-check the public launch API without performing any Rex operation.
    launch := func(ctx context.Context) error {
        _, err := manager.Open(ctx, anchored, "sdk-notes")
        return err
    }
    _ = launch
    _ = rawrkit.TerminalSpec{ID: "sdk-shell", Title: "SDK shell", Argv: []string{"sh"}, Cwd: root}
    _ = manager.ActivateTerminal
    _ = ui.Run
    screen := tcell.NewSimulationScreen("UTF-8")
    if err := screen.Init(); err != nil { panic(err) }
    defer screen.Fini()
    screen.SetSize(40, 8)
    screen.PostEvent(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
    screen.PostEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
    view := &notesView{}
    uiContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    if err := ui.RunScreen(uiContext, screen, view); err != nil { panic(err) }
    glyph, _, _, _ := screen.GetContent(0, 0)
    if !view.activated || view.list.Selected != 1 || glyph != 'S' {
        panic("public UI selection/rendering failed")
    }
    fmt.Println("external rawrkit consumer: custom app API and UI rendering/selection simulation passed")
}
`

func main() {
	goTool := flag.String("go", "go", "Go executable")
	rootFlag := flag.String("root", ".", "rawr checkout root")
	targets := flag.String("targets", runtime.GOOS+"/"+runtime.GOARCH, "comma-separated build targets")
	flag.Parse()
	if err := smoke(*goTool, *rootFlag, *targets); err != nil {
		fmt.Fprintln(os.Stderr, "sdk-smoke:", err)
		os.Exit(1)
	}
}

func smoke(goTool, rootFlag, targets string) error {
	root, err := filepath.Abs(rootFlag)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "rawrkit")); err != nil {
		return fmt.Errorf("public rawrkit package unavailable: %w", err)
	}
	dir, err := os.MkdirTemp("", "rawrkit-sdk-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	mod := fmt.Sprintf("module example.com/rawrkit-consumer-smoke\n\ngo 1.24.0\n\nrequire github.com/metcalfc/rawr v0.0.0\n\nreplace github.com/metcalfc/rawr => %q\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(consumer), 0600); err != nil {
		return err
	}
	for _, value := range strings.Split(targets, ",") {
		parts := strings.Split(strings.TrimSpace(value), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid target %q", value)
		}
		binary := "consumer"
		if parts[0] == "windows" {
			binary += ".exe"
		}
		fmt.Println("External SDK consumer:", value)
		cmd := exec.CommandContext(context.Background(), goTool, "build", "-mod=mod", "-trimpath", "-o", binary, ".")
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
		if err := cmd.Run(); err != nil {
			return err
		}
		if parts[0] == runtime.GOOS && parts[1] == runtime.GOARCH {
			cmd := exec.CommandContext(context.Background(), filepath.Join(dir, binary))
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
		}
	}
	return nil
}
