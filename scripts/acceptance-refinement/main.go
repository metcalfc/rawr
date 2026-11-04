// Creates one isolated native Rex workspace with labelled provider fixtures.
// It never writes terminal input or mutates the user's checkout or sessions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type location struct {
	Root, Session, Anchor, State, Bin, ProviderBin string
	LocalPID                                       int
	Geometry                                       json.RawMessage
	Host, Generation, Version, Endpoint            string
	SessionLabel, AnchorLabel                      string
	Pending, Failure                               string
	PendingBlocks                                  []string
	Apps                                           map[string]rawrkit.Binding
}

var fixtureApps = []string{"exerawr", "filerawr", "ghrawr"}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
func command(args ...string) {
	cmd := exec.Command(args[0], args[1:]...)
	if b, err := cmd.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("%v: %s", err, b))
	}
}
func put(path, body string) { check(os.WriteFile(path, []byte(body), 0600)) }
func fixture(exe string) bool {
	name := filepath.Base(exe)
	if name != "gh" && name != "ssh" && name != "open" && name != "xdg-open" {
		return false
	}
	root := filepath.Dir(filepath.Dir(exe))
	args := os.Args[1:]
	joined := strings.Join(args, " ")
	switch name {
	case "gh":
		switch {
		case len(args) > 1 && args[0] == "pr" && args[1] == "list":
			fmt.Println(`[{"number":42,"title":"Add resource actions (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/pull/42"}]`)
		case len(args) > 1 && args[0] == "issue" && args[1] == "list":
			fmt.Println(`[{"number":17,"title":"Browse terminal references (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/issues/17"}]`)
		case len(args) > 1 && args[0] == "pr" && args[1] == "checks":
			fmt.Println(`[{"name":"Go tests (FIXTURE)","bucket":"pass","state":"SUCCESS","link":"https://github.com/demo/rawr-workspace/actions/runs/1"}]`)
		case len(args) > 1 && args[0] == "pr" && args[1] == "view":
			fmt.Println(`{"number":42,"title":"Add resource actions (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/pull/42"}`)
		case len(args) > 2 && args[0] == "pr" && args[1] == "checkout" && args[2] == "42":
			command("git", "-C", filepath.Join(root, "project"), "checkout", "fixture/pr-42")
			fmt.Println("FIXTURE: switched the throwaway local checkout to fixture/pr-42")
		default:
			fmt.Fprintln(os.Stderr, "unsupported fixture gh request")
			os.Exit(2)
		}
	case "ssh":
		if strings.Contains(joined, "ls --json") {
			fmt.Println(`{"vms":[{"vm_name":"demo-dev","status":"running","ssh_dest":"demo-dev.fixture.invalid","https_url":"https://demo-dev.example.com"},{"vm_name":"demo-test","status":"stopped","ssh_dest":"demo-test.fixture.invalid","https_url":"https://demo-test.example.com"}]}`)
		} else {
			fmt.Println("SIMULATED VM SHELL — local fixture, no SSH/network connection\nPrevious local work is retained in a native Rex tab.")
			for {
				time.Sleep(time.Hour)
			}
		}
	default:
		put(filepath.Join(root, "opened-url.txt"), strings.Join(args, "\n"))
		fmt.Println("FIXTURE browser action recorded")
	}
	return true
}
func geometry(v rex.View, main string) json.RawMessage {
	var node func(*rex.LayoutNode) any
	node = func(n *rex.LayoutNode) any {
		if n == nil {
			return nil
		}
		if n.BlockID != "" {
			return "pane"
		}
		return []any{n.Split.Direction, n.Split.Ratio, node(n.Split.Before), node(n.Split.After)}
	}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, id := range rex.Leaves(l.Layout) {
				if id == main {
					b, _ := json.Marshal(node(l.Layout))
					return b
				}
			}
		}
	}
	panic("main geometry unavailable")
}
func main() {
	self, err := os.Executable()
	check(err)
	if fixture(self) {
		return
	}
	ctx := context.Background()
	cwd, err := os.Getwd()
	check(err)
	path := filepath.Join(cwd, ".acceptance", "refinement", "location.json")
	mode := "setup"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	r := rex.New("", "", "")
	loc := location{}
	shell := ""
	if mode == "setup" {
		if _, err := os.Stat(path); err == nil {
			panic("fixture workspace already recorded; use an action instead of creating duplicates")
		}
		root, err := os.MkdirTemp("/private/tmp", "rawr-refinement-")
		check(err)
		loc = location{Root: filepath.Join(root, "project"), State: filepath.Join(root, "state"), Bin: filepath.Join(cwd, "build", "bin"), ProviderBin: filepath.Join(root, "bin")}
		check(os.MkdirAll(filepath.Join(loc.Root, "src"), 0700))
		check(os.MkdirAll(loc.ProviderBin, 0700))
		b, err := os.ReadFile(self)
		check(err)
		for _, name := range []string{"gh", "ssh", "open", "xdg-open"} {
			check(os.WriteFile(filepath.Join(loc.ProviderBin, name), b, 0700))
		}
		check(os.Setenv("PATH", loc.ProviderBin+string(os.PathListSeparator)+os.Getenv("PATH")))
		put(filepath.Join(loc.Root, "README.md"), "# Rawr resource panes\n\nDEMO: GitHub and VM data are fixtures.\n\nBrowse resources at the sides. Explicit actions drive the center.\nPrevious work stays in native Rex tabs.\n")
		put(filepath.Join(loc.Root, "src", "hello.go"), "package main\n\n// This file belongs to the isolated demo project.\nfunc main() {}\n")
		command("git", "init", "--quiet", "--initial-branch=main", loc.Root)
		command("git", "-C", loc.Root, "remote", "add", "origin", "https://github.com/demo/rawr-workspace.git")
		command("git", "-C", loc.Root, "add", ".")
		command("git", "-C", loc.Root, "-c", "user.name=Rawr Demo", "-c", "user.email=demo@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "Isolated fixture")
		command("git", "-C", loc.Root, "branch", "fixture/pr-42")
		shell, err = exec.LookPath("bash")
		check(err)
	} else {
		check(storage.ReadJSON(path, &loc))
		if loc.Pending != "" {
			panic("incomplete fixture operation " + loc.Pending + "; inspect the saved native IDs before any explicit repair; no automatic retry")
		}
		check(os.Setenv("PATH", loc.ProviderBin+string(os.PathListSeparator)+os.Getenv("PATH")))
	}
	c := model.Context{Name: "DEMO · provider fixtures", Root: loc.Root, Repository: "demo/rawr-workspace", Session: loc.Session, Anchor: loc.Anchor, Scope: rawrkit.ScopeForRoot(loc.Root), StateDir: loc.State, Rex: r.Executable}
	m := catalog.NewManager(r, loc.State, loc.Bin)
	switch mode {
	case "setup":
		check(setupFixture(ctx, r, &loc, path, shell, func(ctx context.Context) error {
			c.Session, c.Anchor = loc.Session, loc.Anchor
			for _, name := range fixtureApps {
				if _, err := m.Open(ctx, c, name); err != nil {
					return err
				}
			}
			p, err := r.Process(ctx, loc.Session, loc.Anchor)
			if err != nil {
				return err
			}
			if p.Child == nil {
				return fmt.Errorf("original local process is not inspectable; pending fixture retained")
			}
			loc.LocalPID = p.Child.PID
			v, err := r.View(ctx, loc.Session)
			if err != nil {
				return err
			}
			loc.Geometry = geometry(v, loc.Anchor)
			snapshot, err := m.Snapshot(ctx, c)
			if err != nil {
				return err
			}
			loc.Apps = snapshot.Bindings
			return nil
		}))
		check(r.Select(ctx, loc.Session, loc.Anchor))
	case "file":
		spec, err := catalog.FileTerminal(loc.Bin, loc.Root, model.FileRequest{Path: filepath.Join(loc.Root, "README.md")})
		check(err)
		check(m.ActivateTerminal(ctx, c, spec))
	case "reload-apps":
		s, err := m.Snapshot(ctx, c)
		check(err)
		check(reloadFixture(ctx, r, &loc, path, s.Bindings, func(ctx context.Context) (map[string]rawrkit.Binding, error) {
			for _, name := range fixtureApps {
				if _, err := m.Open(ctx, c, name); err != nil {
					return nil, err
				}
			}
			snapshot, err := m.Snapshot(ctx, c)
			return snapshot.Bindings, err
		}))
	case "vm":
		spec, err := catalog.Terminal(model.VM{Name: "demo-dev", SSHDest: "demo-dev.fixture.invalid"}, loc.Root)
		check(err)
		check(m.ActivateTerminal(ctx, c, spec))
	case "toggles":
		s, err := m.Snapshot(ctx, c)
		check(err)
		pids := map[string]int{}
		for app, bid := range s.Apps {
			p, err := r.Process(ctx, c.Session, bid)
			check(err)
			pids[app] = p.Child.PID
		}
		for _, app := range []string{"ghrawr", "filerawr", "exerawr"} {
			_, err := m.Hide(ctx, c, app)
			check(err)
		}
		for _, app := range []string{"exerawr", "ghrawr", "filerawr"} {
			result, err := m.Open(ctx, c, app)
			check(err)
			if !result.Reused || result.BlockID != s.Apps[app] {
				panic("toggle duplicated a resource pane")
			}
			p, err := r.Process(ctx, c.Session, result.BlockID)
			check(err)
			if p.Child.PID != pids[app] {
				panic("toggle replaced a resource process")
			}
		}
	case "local":
		_, err := m.LocalTerm(ctx, c)
		check(err)
	case "checkout":
		check(m.RunLocalAction(ctx, c, func(ctx context.Context) error {
			return services.CheckoutPR(ctx, loc.Root, c.Repository, model.PR{Number: 42, State: "OPEN", URL: "https://github.com/demo/rawr-workspace/pull/42"})
		}))
	case "changes":
		put(filepath.Join(loc.Root, "README.md"), "# Rawr resource panes\n\nDEMO: provider fixtures.\n\nUnstaged change for the Git changes view.\n")
		put(filepath.Join(loc.Root, "untracked.txt"), "An untracked fixture file.\n")
	case "verify":
		s, err := m.Snapshot(ctx, c)
		check(err)
		v, err := r.View(ctx, loc.Session)
		check(err)
		p, err := r.Process(ctx, loc.Session, loc.Anchor)
		check(err)
		if p.Child == nil || p.Child.PID != loc.LocalPID {
			panic("original local process changed")
		}
		var before, after any
		check(json.Unmarshal(loc.Geometry, &before))
		check(json.Unmarshal(geometry(v, s.MainBlock), &after))
		if !reflect.DeepEqual(before, after) {
			panic("native geometry changed")
		}
		checks := map[string]any{"original_local_pid_preserved": true, "native_geometry_preserved": true, "app_count": len(s.Apps), "main_block": s.MainBlock, "selected_terminal": s.SelectedTerminal, "scope": "actual native Rex panes; GitHub and VM providers are labelled local fixtures"}
		check(storage.WriteJSON(filepath.Join(cwd, ".acceptance", "refinement", "verification.json"), checks))
		fmt.Println("Native resource workspace verified")
	default:
		panic("unknown fixture action")
	}
	fmt.Printf("Fixture action %s complete; session %s\n", mode, loc.Session)
}
