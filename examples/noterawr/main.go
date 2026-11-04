// noterawr is a minimal, independently buildable Rawrkit example. It needs no
// Rawr project or built-in app registry. Run --tui outside Rex, or run inside Rex
// to open its own native pane. It is an example, not part of the release bundle.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

type notes struct {
	root, message string
	list          ui.List
	area          ui.Rect
}

func (*notes) Init(*ui.Canvas) error   { return nil }
func (*notes) Refresh(context.Context) {}
func (n *notes) Handle(_ context.Context, e tcell.Event) bool {
	if k, ok := e.(*tcell.EventKey); ok && (k.Rune() == 'q' || k.Key() == tcell.KeyEscape) {
		return true
	}
	if _, activate := n.list.Handle(e, 3, n.area); activate {
		n.message = []string{"The app owns its data and actions.", "Rawrkit retains native panes and programs.", "Rawr projects are optional."}[n.list.Selected]
	}
	return false
}
func (n *notes) Draw(c *ui.Canvas) {
	w, h := c.Size()
	c.Text(0, 0, "Note Rawr · Rawrkit example", c.Theme.Title)
	c.Text(0, 1, "Root: "+n.root, c.Theme.Normal)
	n.area = ui.Rect{X: 0, Y: 3, Width: w, Height: max(1, h-6)}
	n.list.Draw(c, n.area, 3, func(i int) string { return []string{"App data", "Native pane lifecycle", "Optional projects"}[i] })
	c.Text(0, h-2, n.message, c.Theme.Normal)
	c.Text(0, h-1, "↑↓ / click / Enter · q quit", c.Theme.Normal)
}

func run() error {
	tui := flag.Bool("tui", false, "run in this terminal")
	contextFile := flag.String("context-file", "", "explicit native launch context")
	root := flag.String("root", "", "anchored root")
	state := flag.String("state-dir", "", "shared app state")
	toggle := flag.Bool("toggle", false, "hide/show the retained native pane")
	rexPath := flag.String("rex", "", "Rex CLI path")
	server := flag.String("server", "", "local Rex server")
	client := flag.String("client", "", "Rex app client")
	flag.Parse()
	ctx := context.Background()
	c := rawrkit.Context{}
	if *contextFile != "" {
		var err error
		c, err = rawrkit.ReadContext(*contextFile)
		if err != nil {
			return err
		}
	} else {
		if *root == "" {
			var err error
			*root, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		abs, err := filepath.Abs(*root)
		if err != nil {
			return err
		}
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		if *state == "" {
			base := os.Getenv("XDG_STATE_HOME")
			if base == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				base = filepath.Join(home, ".local", "state")
			}
			*state = filepath.Join(base, "rexapps")
		}
		c = rawrkit.Context{Name: filepath.Base(abs), Root: abs, Scope: rawrkit.ScopeForRoot(abs), Session: os.Getenv("REX_SESSION"), Anchor: os.Getenv("REX_BLOCK"), StateDir: *state, Rex: *rexPath, Server: *server, Client: *client}
	}
	if *tui {
		return ui.Run(ctx, &notes{root: c.Root})
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m := rawrkit.New(rex.New(c.Rex, c.Server, c.Client), c.StateDir, filepath.Dir(exe), rawrkit.Definition{ID: "noterawr", Title: "Note Rawr", Command: []string{exe}, Placement: rawrkit.PlacementSpec{Direction: "horizontal", Side: "after", Ratio: .75}})
	var result rawrkit.Result
	if *toggle {
		result, err = m.Toggle(ctx, c, "noterawr")
	} else {
		result, err = m.Open(ctx, c, "noterawr")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "noterawr:", err)
		os.Exit(1)
	}
}
