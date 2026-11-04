package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type nativeFake struct {
	t            *testing.T
	view         rex.View
	blocks       map[string]rex.Block
	process      map[string]rex.Process
	closeOnExit  map[string]bool
	calls        []string
	next         int
	viewCalls    int
	changeOnView int
	badCreate    bool
	failMove     bool
}

func fixture(t *testing.T, kinds ...string) (*Manager, Project, *nativeFake) {
	t.Helper()
	dir := t.TempDir()
	f := &nativeFake{t: t, blocks: map[string]rex.Block{}, process: map[string]rex.Process{}}
	c := rex.New("fake-rex", "", " ")
	c.ClientID = ""
	c.Runner = f.run
	m := New(c, filepath.Join(dir, "config"), filepath.Join(dir, "state"), filepath.Join(dir, "bin"))
	if err := os.MkdirAll(m.BinaryDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"filerawr", "ghrawr", "exerawr"} {
		if err := os.WriteFile(filepath.Join(m.BinaryDir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := m.Create("Project with spaces", dir, "metcalfc/rawr", "", kinds)
	if err != nil {
		t.Fatal(err)
	}
	return m, p, f
}
func raw(value any) []byte { data, _ := json.Marshal(value); return data }
func argAfter(args []string, key string) string {
	for i, v := range args {
		if v == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func (f *nativeFake) run(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
	if argAfter(args, "whoami") == "--json" {
		f.calls = append(f.calls, "whoami")
		return raw(rex.Identity{HostID: "host:local", InstanceID: "generation:1", Version: rex.TestedVersion, Endpoint: "unix:/tmp/rex-test"}), nil, nil
	}
	if argAfter(args, "block") == "call" {
		bid := argAfter(args, "-b")
		f.calls = append(f.calls, "process:"+bid)
		proc, ok := f.process[bid]
		if !ok {
			return nil, nil, errors.New("no process")
		}
		return raw(proc), nil, nil
	}
	method := argAfter(args, "call")
	var params map[string]any
	for i, v := range args {
		if v == "call" && i+2 < len(args) {
			if err := json.Unmarshal([]byte(args[i+2]), &params); err != nil {
				f.t.Fatal(err)
			}
			break
		}
	}
	f.calls = append(f.calls, method)
	get := func(key string) string { v, _ := params[key].(string); return v }
	switch method {
	case "session.list":
		list := []rex.Session{}
		if f.view.ID != "" {
			list = append(list, rex.Session{ID: f.view.ID, Label: f.view.Label})
		}
		return raw(map[string]any{"sessions": list}), nil, nil
	case "client.list":
		return raw(map[string]any{"clients": []any{}}), nil, nil
	case "session.focus_block":
		for i, w := range f.view.Windows {
			for _, l := range w.Layers {
				for _, bid := range rex.Leaves(l.Layout) {
					if bid == get("block_id") {
						f.view.ActiveWindowID = w.ID
						f.view.Windows[i].FocusedBlockID = bid
						return raw(map[string]any{}), nil, nil
					}
				}
			}
		}
		return nil, nil, fmt.Errorf("cannot focus hidden block")
	case "session.create":
		f.view = rex.View{ID: "session:test", Label: get("label"), Revision: 1}
		created := rex.Created{SessionID: f.view.ID}
		windows, _ := params["initial_windows"].([]any)
		for i, value := range windows {
			w := value.(map[string]any)
			node := f.createNode(w["layout"].(map[string]any))
			wid := fmt.Sprintf("window:%d", i+1)
			f.view.Windows = append(f.view.Windows, rex.Window{ID: wid, Label: w["window_label"].(string), FocusedBlockID: rex.Leaves(node)[0], Layers: []rex.Layer{{ID: "layer:" + wid, Kind: "tiled", Layout: node}}})
			created.InitialWindows = append(created.InitialWindows, rex.Created{WindowID: wid, BlockIDs: rex.Leaves(node)})
		}
		f.view.ActiveWindowID = f.view.Windows[0].ID
		f.refresh()
		if f.badCreate {
			created.InitialWindows = nil
		}
		return raw(created), nil, nil
	case "session.view":
		f.viewCalls++
		if f.viewCalls == f.changeOnView {
			f.view.Revision++
		}
		f.refresh()
		return raw(f.view), nil, nil
	case "session.list_blocks":
		f.refresh()
		all := []rex.Block{}
		for _, bid := range sortedKeys(f.blocks) {
			all = append(all, f.blocks[bid])
		}
		return raw(map[string]any{"blocks": all}), nil, nil
	case "session.new_split":
		node := f.createNode(params["layout"].(map[string]any))
		ids := rex.Leaves(node)
		if !f.attach(ids[0], get("anchor_id"), get("direction"), get("side"), params["ratio"].(float64)) {
			return nil, nil, errors.New("anchor absent")
		}
		f.view.Revision++
		return raw(rex.Created{BlockIDs: ids}), nil, nil
	case "session.detach_block":
		f.remove(get("block_id"))
		f.view.Revision++
		return raw(map[string]any{}), nil, nil
	case "session.move_block":
		if f.failMove {
			return nil, nil, errors.New("simulated interrupted move")
		}
		bid := get("block_id")
		f.remove(bid)
		if !f.attach(bid, get("anchor_block_id"), get("direction"), get("side"), params["ratio"].(float64)) {
			return nil, nil, errors.New("move anchor absent")
		}
		f.view.Revision++
		return raw(map[string]any{}), nil, nil
	default:
		f.t.Fatalf("unexpected Rex mutation or API: %s %v", method, params)
		return nil, nil, nil
	}
}
func (f *nativeFake) createNode(n map[string]any) *rex.LayoutNode {
	if value, ok := n["block"]; ok {
		b := value.(map[string]any)
		f.next++
		bid := fmt.Sprintf("block:%d", f.next)
		f.blocks[bid] = rex.Block{ID: bid, Label: b["label"].(string), HostID: "host:local", Flavor: "com.superlogical.terminal.shell", Placement: "tiled"}
		if f.closeOnExit == nil {
			f.closeOnExit = map[string]bool{}
		}
		options := b["options"].(map[string]any)
		f.closeOnExit[bid] = options["exit"].(map[string]any)["on_completion"].(bool)
		path := "/bin/sh"
		if argv, ok := options["command"].([]any); ok {
			path = argv[0].(string)
		}
		f.process[bid] = rex.Process{Child: &rex.ProcessInfo{PID: 100 + f.next, Name: filepath.Base(path), Path: path, Argv0: path}}
		return &rex.LayoutNode{BlockID: bid}
	}
	s := n["split"].(map[string]any)
	return &rex.LayoutNode{Split: &rex.Split{ID: "split:test", Direction: s["direction"].(string), Ratio: s["ratio"].(float64), Before: f.createNode(s["before"].(map[string]any)), After: f.createNode(s["after"].(map[string]any))}}
}
func (f *nativeFake) refresh() {
	for bid, b := range f.blocks {
		b.Placement = "detached"
		b.WindowID = ""
		f.blocks[bid] = b
	}
	for wi := range f.view.Windows {
		w := &f.view.Windows[wi]
		for li := range w.Layers {
			l := &w.Layers[li]
			l.Blocks = nil
			for _, bid := range rex.Leaves(l.Layout) {
				b := f.blocks[bid]
				b.Placement = "tiled"
				b.WindowID = w.ID
				f.blocks[bid] = b
				l.Blocks = append(l.Blocks, b)
			}
		}
	}
}
func removeNode(n *rex.LayoutNode, bid string) *rex.LayoutNode {
	if n == nil {
		return nil
	}
	if n.BlockID == bid {
		return nil
	}
	if n.Split == nil {
		return n
	}
	n.Split.Before = removeNode(n.Split.Before, bid)
	n.Split.After = removeNode(n.Split.After, bid)
	if n.Split.Before == nil {
		return n.Split.After
	}
	if n.Split.After == nil {
		return n.Split.Before
	}
	return n
}
func (f *nativeFake) remove(bid string) {
	for wi := range f.view.Windows {
		for li := range f.view.Windows[wi].Layers {
			l := &f.view.Windows[wi].Layers[li]
			l.Layout = removeNode(l.Layout, bid)
		}
	}
	f.refresh()
}
func attachNode(n *rex.LayoutNode, bid, anchor, direction, side string, ratio float64) (*rex.LayoutNode, bool) {
	if n == nil {
		return nil, false
	}
	if n.BlockID == anchor {
		before, after := n, &rex.LayoutNode{BlockID: bid}
		if side == "before" {
			before, after = after, before
		}
		return &rex.LayoutNode{Split: &rex.Split{ID: "split:moved", Direction: direction, Ratio: ratio, Before: before, After: after}}, true
	}
	if n.Split == nil {
		return n, false
	}
	var ok bool
	n.Split.Before, ok = attachNode(n.Split.Before, bid, anchor, direction, side, ratio)
	if ok {
		return n, true
	}
	n.Split.After, ok = attachNode(n.Split.After, bid, anchor, direction, side, ratio)
	return n, ok
}
func (f *nativeFake) attach(bid, anchor, direction, side string, ratio float64) bool {
	for wi := range f.view.Windows {
		for li := range f.view.Windows[wi].Layers {
			l := &f.view.Windows[wi].Layers[li]
			n, ok := attachNode(l.Layout, bid, anchor, direction, side, ratio)
			l.Layout = n
			if ok {
				f.refresh()
				return true
			}
		}
	}
	return false
}
func (f *nativeFake) addUserPane(anchor string) string {
	node := f.createNode(rex.Leaf("arbitrary editor", nil, "/tmp"))
	bid := node.BlockID
	f.attach(bid, anchor, "vertical", "after", .5)
	f.view.Revision++
	return bid
}
func count(calls []string, method string) int {
	n := 0
	for _, call := range calls {
		if call == method {
			n++
		}
	}
	return n
}
func mustOpen(t *testing.T, m *Manager, p Project) Result {
	t.Helper()
	r, err := m.Open(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDefaultIndependentBLayoutAndTypedAppLaunch(t *testing.T) {
	m, p, _ := fixture(t, "files", "ghrawr", "exerawr")
	if got := Leaves(p.Setup[0].Layout); !reflect.DeepEqual(got, []string{"files", "work-main", "github", "exedev"}) {
		t.Fatal(got)
	}
	n := p.Setup[0].Layout
	if n.Direction != "horizontal" || n.Ratio != .2 || n.After.Direction != "horizontal" || n.After.Ratio != .7 || n.After.After.Direction != "vertical" {
		t.Fatal("not independent mockup B")
	}
	args := m.command(p, "github")
	if args[0] != filepath.Join(m.BinaryDir, "ghrawr") || argAfter(args, "--repo") != "metcalfc/rawr" || argAfter(args, "--root") != p.Root || argAfter(args, "--scope") != rawrkit.ScopeForRoot(p.Root) || argAfter(args, "--state-dir") != m.AppsDir {
		t.Fatal(args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "python") {
			t.Fatal(args)
		}
	}
}

func TestLegacySchema2RecipeNamesNormalizeWithoutWriting(t *testing.T) {
	m, p, _ := fixture(t, "ghrex", "filerex", "exerex")
	for role, kind := range map[string]string{"github": "ghrex", "files": "filerex", "exedev": "exerex"} {
		recipe := p.Roles[role]
		recipe.Kind = kind
		p.Roles[role] = recipe
	}
	if err := storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.projectPath(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := m.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for role, kind := range map[string]string{"github": "ghrawr", "files": "filerawr", "exedev": "exerawr"} {
		if loaded.Roles[role].Kind != kind {
			t.Fatal(loaded)
		}
	}
	after, err := os.ReadFile(m.projectPath(p.ID))
	if err != nil || string(before) != string(after) {
		t.Fatal("read rewrote legacy saved setup", err)
	}
	if loaded.Revision != p.Revision || !reflect.DeepEqual(loaded.Setup, p.Setup) {
		t.Fatal("normalization changed deliberately saved topology")
	}
	if got := m.command(loaded, "github")[0]; got != filepath.Join(m.BinaryDir, "ghrawr") {
		t.Fatal(got)
	}
}

func TestLegacyAliasRoleCollisionFailsBeforeRexMutation(t *testing.T) {
	m, p, f := fixture(t, "ghrawr")
	r := p.Roles["github"]
	r.Kind = "ghrex"
	p.Roles["second-github"] = r
	if err := storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "multiple role associations") {
		t.Fatal(err)
	}
	if len(f.calls) > 0 {
		t.Fatal("colliding aliases reached Rex", f.calls)
	}
}

func TestLegacyLiveAppBindingsKeepIDsAndNeverRelaunchOccupiedWork(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("changed=", changed), func(t *testing.T) {
			m, p, f := fixture(t, "ghrawr")
			mustOpen(t, m, p)
			c, err := m.Context(context.Background(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(m.BinaryDir, "ghrex")
			if err = os.WriteFile(legacy, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			o, err := m.readObserved(p)
			if err != nil {
				t.Fatal(err)
			}
			b := o.Roles["github"]
			bid := b.BlockID
			proc := f.process[bid]
			proc.Child.Path = legacy
			proc.Child.Argv0 = legacy
			proc.Child.Name = "ghrex"
			f.process[bid] = proc
			child := *proc.Child
			b.Program = &child
			b.RecipeKind = "ghrex"
			o.Roles["github"] = b
			if err = m.record(p, o); err != nil {
				t.Fatal(err)
			}
			recipe := p.Roles["github"]
			recipe.Kind = "ghrex"
			p.Roles["github"] = recipe
			if err = storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
				t.Fatal(err)
			}
			key := sha256.Sum256([]byte("host:local\x00" + c.Session + "\x00" + c.Scope))
			path := filepath.Join(c.StateDir, hex.EncodeToString(key[:16])+".json")
			var state rawrkit.State
			if err = storage.ReadJSON(path, &state); err != nil {
				t.Fatal(err)
			}
			shared := state.Apps["ghrawr"]
			shared.Path = legacy
			delete(state.Apps, "ghrawr")
			state.Apps["ghrex"] = shared
			if err = storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			if changed {
				proc.Child.PID++
				f.process[bid] = proc
			}
			calls := len(f.calls)
			r, err := m.Restore(context.Background(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if changed && r.Result != "partial" {
				t.Fatal(r)
			}
			if !changed && (r.Result != "complete" || !r.LayoutMatches) {
				t.Fatal(r)
			}
			if count(f.calls[calls:], "session.new_split") != 0 || count(f.calls[calls:], "session.move_block") != 0 || count(f.calls[calls:], "session.detach_block") != 0 {
				t.Fatal("legacy/occupied app was relaunched or moved", f.calls[calls:])
			}
			updated, err := m.readObserved(p)
			if err != nil || updated.Roles["github"].BlockID != bid {
				t.Fatal(updated, err)
			}
		})
	}
}

func TestLegacyAppNameDoesNotAuthorizeNewUnboundProgram(t *testing.T) {
	m, p, f := fixture(t, "ghrawr")
	mustOpen(t, m, p)
	o, err := m.readObserved(p)
	if err != nil {
		t.Fatal(err)
	}
	b := o.Roles["github"]
	proc := f.process[b.BlockID]
	proc.Child.Path = filepath.Join(m.BinaryDir, "ghrex")
	proc.Child.Argv0 = proc.Child.Path
	proc.Child.Name = "ghrex"
	f.process[b.BlockID] = proc
	candidate := m.observeBinding(context.Background(), p, "github", b.BlockID, b.Label, rex.Identity{InstanceID: "generation:1"}, o.SessionID)
	if candidate.PID != 0 || candidate.Program != nil {
		t.Fatal("old executable basename authorized an unbound launch", candidate)
	}
}

func TestProjectCreatedAppsAdoptSharedScope(t *testing.T) {
	m, p, f := fixture(t, "filerawr", "ghrawr", "exerawr")
	mustOpen(t, m, p)
	c, err := m.Context(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := catalog.NewManager(m.Rex, c.StateDir, m.BinaryDir).Snapshot(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Apps) != 3 || s.MainBlock != c.Anchor {
		t.Fatal(s)
	}
	for kind, bid := range s.Apps {
		if f.process[bid].Child.Path != filepath.Join(m.BinaryDir, kind) {
			t.Fatal(kind, bid)
		}
	}
	if count(f.calls, "session.create") != 1 {
		t.Fatal(f.calls)
	}
}

func TestRestoreAdoptsStandaloneReplacementWithoutDuplicate(t *testing.T) {
	m, p, f := fixture(t, "ghrawr")
	mustOpen(t, m, p)
	c, err := m.Context(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	o, err := m.readObserved(p)
	if err != nil {
		t.Fatal(err)
	}
	old := o.Roles["github"].BlockID
	f.remove(old)
	delete(f.blocks, old)
	delete(f.process, old)
	pm := catalog.NewManager(m.Rex, c.StateDir, m.BinaryDir)
	replacement, err := pm.Open(context.Background(), c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Reused || replacement.BlockID == old {
		t.Fatal(replacement)
	}
	calls := len(f.calls)
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || r.Result != "complete" || !r.LayoutMatches {
		t.Fatal(r, err)
	}
	if count(f.calls[calls:], "session.new_split") != 0 || len(f.view.Blocks()) != 2 {
		t.Fatal("project duplicated standalone replacement", f.calls[calls:])
	}
	o, err = m.readObserved(p)
	if err != nil || o.Roles["github"].BlockID != replacement.BlockID {
		t.Fatal(o, err)
	}
}

func TestProjectSyncPreservesDetachedOldSurfaceAndChangedReplacement(t *testing.T) {
	for _, detached := range []bool{true, false} {
		t.Run(fmt.Sprint("detached-old=", detached), func(t *testing.T) {
			m, p, f := fixture(t, "ghrawr")
			mustOpen(t, m, p)
			c, err := m.Context(context.Background(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			o, err := m.readObserved(p)
			if err != nil {
				t.Fatal(err)
			}
			old := o.Roles["github"].BlockID
			f.remove(old)
			if !detached {
				delete(f.blocks, old)
				delete(f.process, old)
			}
			newNode := f.createNode(rex.Leaf("shared-returned-app", m.command(p, "github"), p.Root))
			bid := newNode.BlockID
			f.attach(bid, c.Anchor, "horizontal", "after", .7)
			key := sha256.Sum256([]byte("host:local\x00" + c.Session + "\x00" + c.Scope))
			path := filepath.Join(c.StateDir, hex.EncodeToString(key[:16])+".json")
			var state rawrkit.State
			if err = storage.ReadJSON(path, &state); err != nil {
				t.Fatal(err)
			}
			proc := f.process[bid]
			state.Apps["ghrawr"] = rawrkit.Binding{BlockID: bid, Label: f.blocks[bid].Label, PID: proc.Child.PID, Path: proc.Child.Path}
			if err = storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			if !detached {
				proc.Child.PID++
				f.process[bid] = proc
			}
			calls := len(f.calls)
			r, err := m.Restore(context.Background(), p.ID)
			if detached && err == nil {
				t.Fatal("accepted simultaneous detached old ID and new binding")
			}
			if !detached && (err != nil || r.Result != "partial") {
				t.Fatal(r, err)
			}
			if count(f.calls[calls:], "session.new_split") != 0 || count(f.calls[calls:], "session.detach_block") != 0 {
				t.Fatal("changed occupied work was mutated", f.calls[calls:])
			}
		})
	}
}

func TestSaveRecognizesSharedVMCenterAndPreservesPreviousWork(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	c, err := m.Context(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := c.Anchor
	center := f.addUserPane(old)
	proc := f.process[center]
	proc.Child.Path = "/usr/bin/ssh"
	proc.Child.Argv0 = "/usr/bin/ssh"
	proc.Child.Name = "ssh"
	f.process[center] = proc
	vm := model.VM{Name: "selected-vm", SSHDest: "selected-vm.exe.xyz", Status: "running"}
	spec := rawrkit.TerminalSpec{ID: "exe:" + vm.Name, Title: vm.Name, Argv: []string{"ssh", vm.SSHDest}, Cwd: p.Root, Metadata: map[string]string{"provider": "exe.dev", "name": vm.Name, "ssh_dest": vm.SSHDest}}
	s := rawrkit.State{Schema: 1, Context: c, Host: "host:local", Generation: "generation:1", MainBlock: center, Selected: spec.ID, Apps: map[string]rawrkit.Binding{}, Terminals: map[string]rawrkit.TerminalEntry{spec.ID: {Spec: spec, Binding: rawrkit.Binding{BlockID: center, Label: f.blocks[center].Label, PID: proc.Child.PID, Path: proc.Child.Path}}}, Hidden: map[string]rawrkit.Placement{}, Placements: map[string]rawrkit.Placement{}}
	key := sha256.Sum256([]byte("host:local\x00" + c.Session + "\x00" + c.Scope))
	path := filepath.Join(c.StateDir, hex.EncodeToString(key[:16])+".json")
	if err = storage.WriteJSON(path, s); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Save(context.Background(), p.ID, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := m.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Roles["work-main"].Kind != "vm-shell" || len(saved.Roles["work-main"].Argv) < 2 {
		t.Fatal(saved)
	}
	o, err := m.readObserved(saved)
	if err != nil || o.Roles["work-main"].BlockID != center {
		t.Fatal(o, err)
	}
	foundPrevious := false
	for role, b := range o.Roles {
		if b.BlockID == old && saved.Roles[role].Kind == "shell" {
			foundPrevious = true
		}
	}
	if !foundPrevious || f.process[old].Child == nil {
		t.Fatal("previous local work not retained as placeholder")
	}
}

func TestSaveGenericSelectedTerminalUsesLiteralCommandRecipe(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	c, err := m.Context(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	center := f.addUserPane(c.Anchor)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	proc := f.process[center]
	proc.Child.Path = exe
	proc.Child.Argv0 = exe
	proc.Child.Name = filepath.Base(exe)
	f.process[center] = proc
	spec := rawrkit.TerminalSpec{ID: "fixture-terminal", Title: "Third-party terminal", Argv: []string{exe, "literal space", "$(literal-not-shell)"}, Cwd: p.Root, Metadata: map[string]string{"provider": "fixture"}}
	state := rawrkit.State{Schema: 1, Context: c, Host: "host:local", Generation: "generation:1", MainBlock: center, Selected: spec.ID, Apps: map[string]rawrkit.Binding{}, Terminals: map[string]rawrkit.TerminalEntry{spec.ID: {Spec: spec, Binding: rawrkit.Binding{BlockID: center, Label: f.blocks[center].Label, PID: proc.Child.PID, Path: exe}}}, Hidden: map[string]rawrkit.Placement{}, Placements: map[string]rawrkit.Placement{}}
	key := sha256.Sum256([]byte("host:local\x00" + c.Session + "\x00" + c.Scope))
	path := filepath.Join(c.StateDir, hex.EncodeToString(key[:16])+".json")
	if err = storage.WriteJSON(path, state); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Save(context.Background(), p.ID, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := m.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	recipe := saved.Roles["work-main"]
	if recipe.Kind != "command" || !reflect.DeepEqual(recipe.Argv, spec.Argv) || recipe.Cwd != spec.Cwd {
		t.Fatal(recipe)
	}
}

func TestLinkIsExplicitMetadataWithoutRexMutation(t *testing.T) {
	m, p, f := fixture(t)
	linked, err := m.Link(context.Background(), p.ID, model.VM{Name: "my-vm"})
	if err != nil {
		t.Fatal(err)
	}
	if linked.VM != "my-vm" || linked.Revision != 1 || len(f.calls) > 0 {
		t.Fatal(linked, f.calls)
	}
	saved, err := m.Get(p.ID)
	if err != nil || saved.VM != linked.VM || !reflect.DeepEqual(saved.Setup, p.Setup) {
		t.Fatal(saved, err)
	}
}
func TestOpenLiveProjectPreservesTemporaryArrangement(t *testing.T) {
	m, p, f := fixture(t)
	first := mustOpen(t, m, p)
	if !first.Created || !first.LayoutMatches {
		t.Fatal(first)
	}
	main := rex.Leaves(f.view.Windows[0].Layers[0].Layout)[0]
	f.addUserPane(main)
	arrangement := string(raw(f.view.Windows))
	calls := len(f.calls)
	second := mustOpen(t, m, p)
	if second.Created || string(raw(f.view.Windows)) != arrangement {
		t.Fatal("open changed live panes")
	}
	if count(f.calls, "session.create") != 1 || count(f.calls[calls:], "session.detach_block") != 0 {
		t.Fatal(f.calls)
	}
	saved, err := m.Get(p.ID)
	if err != nil || saved.Revision != 0 || len(Leaves(saved.Setup[0].Layout)) != 1 {
		t.Fatal(saved, err)
	}
}
func TestAmbiguousCreationIsJournaledAndNotRetried(t *testing.T) {
	m, p, f := fixture(t)
	f.badCreate = true
	if _, err := m.Open(context.Background(), p.ID); err == nil {
		t.Fatal("wanted creation error")
	}
	before, err := os.ReadFile(m.statePath(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Open(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal(err)
	}
	if count(f.calls, "session.create") != 1 {
		t.Fatal(f.calls)
	}
	if _, err = m.Plan(context.Background(), p.ID); err == nil {
		t.Fatal("plan accepted pending operation")
	}
	after, _ := os.ReadFile(m.statePath(p.ID))
	if string(before) != string(after) {
		t.Fatal("plan rewrote pending mapping")
	}
}
func TestDeliberateSaveUsesPlaceholdersAndRevisionConflict(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	main := rex.Leaves(f.view.Windows[0].Layers[0].Layout)[0]
	extra := f.addUserPane(main)
	r, err := m.Save(context.Background(), p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 1 || len(r.Notices) < 2 {
		t.Fatal(r)
	}
	saved, err := m.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	roles := Leaves(saved.Setup[0].Layout)
	if len(roles) != 2 || roles[0] != "work-main" || saved.Roles[roles[1]].Kind != "shell" || len(saved.Roles[roles[1]].Argv) != 0 {
		t.Fatal(saved)
	}
	o, err := m.readObserved(saved)
	if err != nil || o.Roles[roles[1]].BlockID != extra {
		t.Fatal(o, err)
	}
	if _, err = os.Stat(m.projectPath(p.ID) + ".previous"); err != nil {
		t.Fatal(err)
	}
	calls := len(f.calls)
	expected := 0
	if _, err = m.Save(context.Background(), p.ID, &expected); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal(err)
	}
	if len(f.calls) != calls {
		t.Fatal("stale save contacted Rex")
	}
}
func TestSaveRejectsConcurrentRexLayoutChange(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	f.changeOnView = f.viewCalls + 3
	if _, err := m.Save(context.Background(), p.ID, nil); err == nil || !strings.Contains(err.Error(), "changed during save") {
		t.Fatal(err)
	}
	saved, err := m.Get(p.ID)
	if err != nil || saved.Revision != 0 {
		t.Fatal(saved, err)
	}
}
func TestRestorePreservesExtraLiveWork(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	main := rex.Leaves(f.view.Windows[0].Layers[0].Layout)[0]
	extra := f.addUserPane(main)
	before := string(raw(f.view.Windows))
	calls := len(f.calls)
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != "partial" || r.LayoutMatches || len(r.Conflicts) == 0 {
		t.Fatal(r)
	}
	if string(raw(f.view.Windows)) != before || f.process[extra].Child == nil {
		t.Fatal("user work changed")
	}
	for _, method := range []string{"session.new_split", "session.detach_block", "session.move_block"} {
		if count(f.calls[calls:], method) != 0 {
			t.Fatal(f.calls[calls:])
		}
	}
}
func commandProject(t *testing.T) (*Manager, Project, *nativeFake) {
	m, p, f := fixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.Roles["worker"] = Recipe{Kind: "command", Argv: []string{exe, "literal space", "$(not-shell-code)"}, Cwd: p.Root, Host: "local", Reopen: "explicit"}
	p.Setup = []Tab{{Name: "Workspace", Layout: split("horizontal", .6, leaf("work-main"), leaf("worker"))}}
	if err = storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
		t.Fatal(err)
	}
	mustOpen(t, m, p)
	return m, p, f
}
func TestRestoreChangedProgramDoesNotRelaunch(t *testing.T) {
	m, p, f := commandProject(t)
	o, _ := m.readObserved(p)
	bid := o.Roles["worker"].BlockID
	proc := f.process[bid]
	proc.Child.PID++
	proc.Child.Path = "/usr/bin/vim"
	f.process[bid] = proc
	calls := len(f.calls)
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || r.Result != "partial" {
		t.Fatal(r, err)
	}
	if count(f.calls[calls:], "session.new_split") != 0 || count(f.calls[calls:], "session.detach_block") != 0 {
		t.Fatal(f.calls[calls:])
	}
}
func TestRestoreReusesDetachedNativeSurfaceAndProcess(t *testing.T) {
	m, p, f := commandProject(t)
	o, _ := m.readObserved(p)
	bid := o.Roles["worker"].BlockID
	pid := f.process[bid].Child.PID
	f.remove(bid)
	f.view.Revision++
	calls := len(f.calls)
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || r.Result != "complete" || !r.LayoutMatches {
		t.Fatal(r, err)
	}
	if count(f.calls[calls:], "session.new_split") != 0 || f.process[bid].Child.PID != pid {
		t.Fatal("detached work relaunched")
	}
	if !reflect.DeepEqual(rex.Leaves(f.view.Windows[0].Layers[0].Layout), []string{o.Roles["work-main"].BlockID, bid}) {
		t.Fatal(f.view)
	}
}
func TestRestoreGenuinelyMissingProgramCreatesNewNativePane(t *testing.T) {
	m, p, f := commandProject(t)
	o, _ := m.readObserved(p)
	old := o.Roles["worker"].BlockID
	f.remove(old)
	delete(f.blocks, old)
	delete(f.process, old)
	calls := len(f.calls)
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || r.Result != "complete" || !r.LayoutMatches {
		t.Fatal(r, err)
	}
	if count(f.calls[calls:], "session.new_split") != 1 {
		t.Fatal(f.calls[calls:])
	}
	updated, _ := m.readObserved(p)
	if updated.Roles["worker"].BlockID == old || updated.LayoutPending || updated.Pending != nil {
		t.Fatal(updated)
	}
}
func TestRestoreInterruptionLeavesDurableNoRetryJournal(t *testing.T) {
	m, p, f := commandProject(t)
	f.view.Windows[0].Layers[0].Layout.Split.Ratio = .4
	f.view.Revision++
	f.failMove = true
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || r.Result != "partial" {
		t.Fatal(r, err)
	}
	o, _ := m.readObserved(p)
	if !o.LayoutPending {
		t.Fatal("no native layout journal")
	}
	calls := len(f.calls)
	if _, err = m.Restore(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal(err)
	}
	if count(f.calls[calls:], "session.move_block") != 0 || f.process[o.Roles["worker"].BlockID].Child == nil {
		t.Fatal("retried or lost work")
	}
}
func TestRestoreRebuildsNestedNativeTopologyWithoutChangingProcesses(t *testing.T) {
	m, p, f := commandProject(t)
	for _, role := range []string{"third", "fourth"} {
		p.Roles[role] = p.Roles["worker"]
	}
	p.Setup[0].Layout = split("horizontal", .2, leaf("third"), split("horizontal", .7, leaf("work-main"), split("vertical", .5, leaf("worker"), leaf("fourth"))))
	if err := storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
		t.Fatal(err)
	}
	// Close the fake session, then open the new saved topology.
	f.view = rex.View{}
	f.blocks = map[string]rex.Block{}
	f.process = map[string]rex.Process{}
	mustOpen(t, m, p)
	pids := map[string]int{}
	for bid, proc := range f.process {
		pids[bid] = proc.Child.PID
	}
	root := f.view.Windows[0].Layers[0].Layout
	root.Split.Direction = "vertical"
	root.Split.Ratio = .6
	root.Split.After.Split.Ratio = .4
	f.view.Revision++
	r, err := m.Restore(context.Background(), p.ID)
	if err != nil || !r.LayoutMatches || r.Result != "complete" {
		t.Fatal(r, err)
	}
	for bid, pid := range pids {
		if f.process[bid].Child.PID != pid {
			t.Fatal("program changed")
		}
	}
}
func TestLegacyAndCorruptDefinitionsFailBeforeRexMutation(t *testing.T) {
	m, p, f := fixture(t)
	p.Schema = 1
	if err := storage.WriteJSON(m.projectPath(p.ID), p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "schema 1") {
		t.Fatal(err)
	}
	if len(f.calls) > 0 {
		t.Fatal(f.calls)
	}
	p.Schema = Schema
	p.Setup[0].Layout = split("horizontal", math.NaN(), leaf("work-main"), leaf("work-main"))
	if err := Validate(p); err == nil {
		t.Fatal("accepted NaN layout")
	}
}
func TestDifferentRuntimeRootCannotDuplicateLiveProject(t *testing.T) {
	m, p, f := fixture(t)
	mustOpen(t, m, p)
	other := New(m.Rex, m.ConfigDir, filepath.Join(t.TempDir(), "other-state"), m.BinaryDir)
	calls := len(f.calls)
	if _, err := other.Open(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "runtime state") {
		t.Fatal(err)
	}
	if _, err := other.Plan(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "runtime state") {
		t.Fatal(err)
	}
	if len(f.calls) != calls {
		t.Fatal("contacted Rex despite wrong runtime root")
	}
}
