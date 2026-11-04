package rawrkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

// fakeNative implements native surface operations independently of pane policy.
// It retains detached blocks and their process identities just as Rex does.
type fakeNative struct {
	t         *testing.T
	identity  rex.Identity
	view      rex.View
	blocks    map[string]rex.Block
	processes map[string]rex.Process
	calls     []string
	seq       int
	lostReply string
}

func newFakeManager(t *testing.T) (*Manager, Context, *fakeNative) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin ; $(literal)")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ghrawr", "filerawr", "exerawr"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("test executable"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	i := rex.Identity{HostID: "host", InstanceID: "generation", Version: rex.TestedVersion, Endpoint: "unix:/tmp/rex fake"}
	f := &fakeNative{t: t, identity: i, blocks: map[string]rex.Block{}, processes: map[string]rex.Process{}, view: rex.View{ID: "session", Revision: 1, ActiveWindowID: "work", Windows: []rex.Window{{ID: "work", FocusedBlockID: "main", Layers: []rex.Layer{{ID: "layer", Kind: "tiled", Layout: &rex.LayoutNode{BlockID: "main"}}}}}}}
	f.blocks["main"] = rex.Block{ID: "main", Label: "my running editor", HostID: i.HostID, Creator: "com.superlogical.terminal", Flavor: "com.superlogical.terminal.shell"}
	f.processes["main"] = rex.Process{Child: &rex.ProcessInfo{PID: 100, Path: "/bin/zsh"}, Foreground: &rex.ProcessInfo{PID: 101, Path: "/usr/bin/vim"}}
	c := rex.New("/tmp/rex executable ; $(literal)", "unix:/tmp/rex socket", "")
	c.Runner = f.run
	manager := New(c, filepath.Join(dir, "state"), bin,
		Definition{ID: "ghrawr", Aliases: []string{"ghrex"}},
		Definition{ID: "filerawr", Aliases: []string{"filerex"}, Placement: PlacementSpec{Direction: "horizontal", Side: "before", Ratio: .2, WholeLayer: true}},
		Definition{ID: "exerawr", Aliases: []string{"exerex"}, Placement: PlacementSpec{Direction: "vertical", Side: "after", Ratio: .5, AnchorApp: "ghrawr"}})

	context := Context{Root: dir, Scope: "project:stable", Session: "session", Anchor: "main", Repository: "owner/repo"}
	return manager, context, f
}
func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func (f *fakeNative) viewSnapshot() rex.View {
	for wi := range f.view.Windows {
		w := &f.view.Windows[wi]
		for li := range w.Layers {
			l := &w.Layers[li]
			l.Blocks = nil
			for _, id := range rex.Leaves(l.Layout) {
				b := f.blocks[id]
				b.WindowID = w.ID
				l.Blocks = append(l.Blocks, b)
			}
		}
	}
	return f.view
}
func (f *fakeNative) inventory() []rex.Block {
	placed := f.viewSnapshot().Blocks()
	list := []rex.Block{}
	for id, b := range f.blocks {
		if v, ok := placed[id]; ok {
			b.WindowID = v.WindowID
			b.Placement = "placed"
		} else {
			b.Placement = "detached"
		}
		list = append(list, b)
	}
	return list
}
func (f *fakeNative) run(_ context.Context, _ string, argv []string) ([]byte, []byte, error) {
	for j, arg := range argv {
		if arg == "whoami" {
			f.calls = append(f.calls, "whoami")
			return encode(f.identity), nil, nil
		}
		if arg == "block" && j+1 < len(argv) && argv[j+1] == "call" {
			bid := ""
			for k := j + 2; k+1 < len(argv); k++ {
				if argv[k] == "-b" {
					bid = argv[k+1]
				}
			}
			f.calls = append(f.calls, "block.process")
			p, ok := f.processes[bid]
			if !ok {
				return nil, nil, fmt.Errorf("missing process %s", bid)
			}
			return encode(p), nil, nil
		}
		if arg == "api" && j+3 < len(argv) && argv[j+1] == "call" {
			method := argv[j+2]
			f.calls = append(f.calls, method)
			var args map[string]any
			if err := json.Unmarshal([]byte(argv[j+3]), &args); err != nil {
				f.t.Fatal(err)
			}
			result, err := f.call(method, args)
			if f.lostReply == method {
				f.lostReply = ""
				return nil, []byte("lost reply after mutation"), errors.New("deadline exceeded")
			}
			return encode(result), nil, err
		}
	}
	return nil, nil, fmt.Errorf("unexpected Rex argv: %q", argv)
}
func (f *fakeNative) count(method string) int {
	n := 0
	for _, m := range f.calls {
		if m == method {
			n++
		}
	}
	return n
}
func (f *fakeNative) split(direction string, ratio float64, before, after *rex.LayoutNode) *rex.LayoutNode {
	f.seq++
	return &rex.LayoutNode{Split: &rex.Split{ID: fmt.Sprintf("split-%d", f.seq), Direction: direction, Ratio: ratio, Before: before, After: after}}
}
func replaceNode(n *rex.LayoutNode, id string, with *rex.LayoutNode) *rex.LayoutNode {
	if n == nil {
		return nil
	}
	if n.BlockID == id || (n.Split != nil && n.Split.ID == id) {
		return with
	}
	if n.Split != nil {
		n.Split.Before = replaceNode(n.Split.Before, id, with)
		n.Split.After = replaceNode(n.Split.After, id, with)
	}
	return n
}
func removeLeaf(n *rex.LayoutNode, id string) *rex.LayoutNode {
	if n == nil || n.BlockID == id {
		return nil
	}
	if n.Split == nil {
		return n
	}
	n.Split.Before = removeLeaf(n.Split.Before, id)
	n.Split.After = removeLeaf(n.Split.After, id)
	if n.Split.Before == nil {
		return n.Split.After
	}
	if n.Split.After == nil {
		return n.Split.Before
	}
	return n
}
func (f *fakeNative) detach(id string) {
	for wi := range f.view.Windows {
		for li := range f.view.Windows[wi].Layers {
			l := &f.view.Windows[wi].Layers[li]
			l.Layout = removeLeaf(l.Layout, id)
		}
	}
}
func (f *fakeNative) addBlock(spec any) string {
	b := spec.(map[string]any)["block"].(map[string]any)
	f.seq++
	id := fmt.Sprintf("block-%d", f.seq)
	label := b["label"].(string)
	options := b["options"].(map[string]any)
	path := "/bin/zsh"
	if argv, ok := options["command"].([]any); ok && len(argv) > 0 {
		path = argv[0].(string)
		if options["shell"] != "none" || options["login"] != false {
			f.t.Fatal("app command was not literal argv")
		}
	}
	f.blocks[id] = rex.Block{ID: id, Label: label, HostID: f.identity.HostID, Creator: "com.superlogical.terminal", Flavor: "com.superlogical.terminal.shell"}
	f.processes[id] = rex.Process{Child: &rex.ProcessInfo{PID: 1000 + f.seq, Path: path}}
	return id
}
func (f *fakeNative) attach(anchor, id, direction, side string, ratio float64) bool {
	var target *rex.LayoutNode
	var find func(*rex.LayoutNode)
	find = func(n *rex.LayoutNode) {
		if n == nil {
			return
		}
		if n.BlockID == anchor || (n.Split != nil && n.Split.ID == anchor) {
			target = n
			return
		}
		if n.Split != nil {
			find(n.Split.Before)
			find(n.Split.After)
		}
	}
	for wi := range f.view.Windows {
		for li := range f.view.Windows[wi].Layers {
			l := &f.view.Windows[wi].Layers[li]
			find(l.Layout)
			if target != nil {
				before, after := target, &rex.LayoutNode{BlockID: id}
				if side == "before" {
					before, after = after, before
				}
				l.Layout = replaceNode(l.Layout, anchor, f.split(direction, ratio, before, after))
				return true
			}
		}
	}
	return false
}
func (f *fakeNative) call(method string, a map[string]any) (any, error) {
	str := func(k string) string { v, _ := a[k].(string); return v }
	switch method {
	case "session.view":
		return f.viewSnapshot(), nil
	case "session.list_blocks":
		return map[string]any{"blocks": f.inventory()}, nil
	case "session.focus_block":
		id := str("block_id")
		for wi := range f.view.Windows {
			w := &f.view.Windows[wi]
			for _, l := range w.Layers {
				if contains(l.Layout, id) {
					w.FocusedBlockID = id
					f.view.ActiveWindowID = w.ID
				}
			}
		}
	case "session.new_split":
		id := f.addBlock(a["layout"])
		if !f.attach(str("anchor_id"), id, str("direction"), str("side"), a["ratio"].(float64)) {
			return nil, errors.New("missing split anchor")
		}
		f.view.Revision++
		return rex.Created{BlockIDs: []string{id}, Revision: f.view.Revision}, nil
	case "session.detach_block":
		f.detach(str("block_id"))
	case "session.move_block":
		id := str("block_id")
		f.detach(id)
		if !f.attach(str("anchor_block_id"), id, str("direction"), str("side"), a["ratio"].(float64)) {
			return nil, errors.New("missing move anchor")
		}
	case "session.new_window":
		id := f.addBlock(a["layout"])
		f.seq++
		wid := fmt.Sprintf("archive-%d", f.seq)
		f.view.Windows = append(f.view.Windows, rex.Window{ID: wid, Label: str("window_label"), FocusedBlockID: id, Layers: []rex.Layer{{ID: "layer-" + wid, Kind: "tiled", Layout: &rex.LayoutNode{BlockID: id}}}})
		f.view.Revision++
		return rex.Created{BlockIDs: []string{id}, WindowID: wid, Revision: f.view.Revision}, nil
	case "session.swap_blocks":
		first, second := str("block_id"), str("other_block_id")
		for wi := range f.view.Windows {
			w := &f.view.Windows[wi]
			for li := range w.Layers {
				l := &w.Layers[li]
				l.Layout = replaceNode(l.Layout, first, &rex.LayoutNode{BlockID: "swap-sentinel"})
				l.Layout = replaceNode(l.Layout, second, &rex.LayoutNode{BlockID: first})
				l.Layout = replaceNode(l.Layout, "swap-sentinel", &rex.LayoutNode{BlockID: second})
			}
		}
	case "session.set_window_label":
		for wi := range f.view.Windows {
			if f.view.Windows[wi].ID == str("window_id") {
				f.view.Windows[wi].Label = str("label")
			}
		}
	default:
		return nil, fmt.Errorf("forbidden or unexpected method %s", method)
	}
	f.view.Revision++
	return map[string]any{"revision": f.view.Revision}, nil
}
func (f *fakeNative) assertNoDestructiveCalls(t *testing.T) {
	t.Helper()
	for _, method := range f.calls {
		if strings.Contains(method, "input") || strings.Contains(method, "close") || strings.Contains(method, "resize") {
			t.Fatalf("unexpected terminal mutation: %s", method)
		}
	}
}

func TestRepeatedOpenReusesAndToggleRetainsProcess(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	first, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	pid := f.processes[first.BlockID].Child.PID
	second, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || first.BlockID != second.BlockID || f.count("session.new_split") != 1 {
		t.Fatal("repeat open duplicated tool")
	}
	hidden, err := m.Toggle(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Visible || f.blocks[first.BlockID].ID == "" || f.processes[first.BlockID].Child.PID != pid {
		t.Fatal("hide destroyed process")
	}
	shown, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	if !shown.Reused || !shown.Visible || shown.BlockID != first.BlockID || f.count("session.new_split") != 1 || f.processes[first.BlockID].Child.PID != pid {
		t.Fatal("show relaunched retained process")
	}
	f.assertNoDestructiveCalls(t)
}

func TestChangedProcessGenerationAndOwnershipPreserveExistingWork(t *testing.T) {
	for _, change := range []string{"pid", "path", "generation", "host", "label"} {
		t.Run(change, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			opened, err := m.Open(ctx, c, "ghrawr")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "pid":
				f.processes[opened.BlockID].Child.PID++
			case "path":
				f.processes[opened.BlockID].Child.Path = "/bin/zsh"
			case "generation":
				f.identity.InstanceID = "new-generation"
			case "host":
				b := f.blocks[opened.BlockID]
				b.HostID = "another-host"
				f.blocks[opened.BlockID] = b
			case "label":
				b := f.blocks[opened.BlockID]
				b.Label = "my unrelated work"
				f.blocks[opened.BlockID] = b
			}
			before := f.count("session.new_split")
			if _, err := m.Toggle(ctx, c, "ghrawr"); err == nil {
				t.Fatalf("accepted changed %s", change)
			}
			if f.count("session.new_split") != before || f.count("session.detach_block") != 0 {
				t.Fatal("changed work was recreated or hidden")
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestLostCreationReplyLeavesJournalAndBlocksSecondCreate(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	f.lostReply = "session.new_split"
	if _, err := m.Open(ctx, c, "exerawr"); err == nil {
		t.Fatal("expected lost creation reply")
	}
	if len(f.blocks) != 2 {
		t.Fatal("fixture did not simulate successful remote creation")
	}
	if _, err := m.Open(ctx, c, "exerawr"); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("pending operation did not block retry: %v", err)
	}
	if f.count("session.new_split") != 1 {
		t.Fatal("ambiguous creation retried")
	}
	snapshot, err := m.Snapshot(ctx, c)
	if err != nil || snapshot.Pending == nil || snapshot.Pending.Label == "" {
		t.Fatalf("missing reviewable journal: %+v %v", snapshot, err)
	}
	f.assertNoDestructiveCalls(t)
}

func TestManagedTerminalSwapRetainsLocalWorkAndReusesPreviousProcess(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	if _, err := m.Open(ctx, c, "filerawr"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(ctx, c, "ghrawr"); err != nil {
		t.Fatal(err)
	}
	initial := clean(f.viewSnapshot().Windows[0].Layers[0].Layout)
	localProcess := encode(f.processes["main"])
	one := TerminalSpec{ID: "task:one", Title: "Task one", Argv: []string{"/bin/sleep", "123"}, Cwd: c.Root}
	two := TerminalSpec{ID: "task:two", Title: "Task two", Argv: []string{"/bin/sleep", "456"}, Cwd: c.Root}
	if err := m.ActivateTerminal(ctx, c, one); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	oneBlock := snapshot.MainBlock
	onePID := f.processes[oneBlock].Child.PID
	if snapshot.SelectedTerminal == nil || snapshot.SelectedTerminal.ID != "task:one" || f.viewSnapshot().Blocks()[oneBlock].WindowID != "work" || f.viewSnapshot().Blocks()["main"].WindowID == "work" {
		t.Fatalf("VM not swapped into center: %+v", snapshot)
	}
	if err := m.ActivateTerminal(ctx, c, two); err != nil {
		t.Fatal(err)
	}
	if err := m.ActivateTerminal(ctx, c, one); err != nil {
		t.Fatal(err)
	}
	if f.count("session.new_window") != 2 || f.processes[oneBlock].Child.PID != onePID {
		t.Fatal("switching back relaunched SSH")
	}
	local, err := m.LocalTerm(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if local.BlockID != "main" || !local.Reused || !reflect.DeepEqual(localProcess, encode(f.processes["main"])) {
		t.Fatal("original local live work was replaced")
	}
	if !equivalent(initial, f.viewSnapshot().Windows[0].Layers[0].Layout) {
		t.Fatal("VM/local swaps changed workspace geometry")
	}
	f.assertNoDestructiveCalls(t)
}

func TestMixedToolTogglesRestoreIDsAndLayout(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, kind := range []string{"ghrawr", "exerawr", "filerawr"} {
		out, err := m.Open(ctx, c, kind)
		if err != nil {
			t.Fatal(err)
		}
		ids[kind] = out.BlockID
	}
	original := clean(f.viewSnapshot().Windows[0].Layers[0].Layout)
	for _, kind := range []string{"ghrawr", "filerawr", "exerawr", "ghrawr", "exerawr", "filerawr"} {
		if _, err := m.Toggle(ctx, c, kind); err != nil {
			t.Fatalf("toggle %s: %v", kind, err)
		}
	}
	if !equivalent(original, f.viewSnapshot().Windows[0].Layers[0].Layout) {
		t.Fatal("mixed toggles changed original geometry")
	}
	snapshot, err := m.Snapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Hidden) != 0 || !reflect.DeepEqual(ids, snapshot.Apps) || f.count("session.new_split") != 3 {
		t.Fatalf("toggles changed app identities: %+v", snapshot)
	}
	f.assertNoDestructiveCalls(t)
}

func TestExternallyDetachedPaneIsPreserved(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	out, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	f.detach(out.BlockID)
	f.view.Revision++
	if _, err := m.Open(ctx, c, "ghrawr"); err == nil || !strings.Contains(err.Error(), "outside these controls") {
		t.Fatalf("unexpected adoption: %v", err)
	}
	if f.count("session.new_split") != 1 || f.count("session.move_block") != 0 {
		t.Fatal("externally detached work was moved")
	}
}

func TestStateContextMismatchDoesNotMutate(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	if _, err := m.Open(ctx, c, "ghrawr"); err != nil {
		t.Fatal(err)
	}
	path := statePath(m.StateDir, f.identity, c)
	var state State
	if err := storage.ReadJSON(path, &state); err != nil {
		t.Fatal(err)
	}
	state.Context.Root = t.TempDir()
	if err := storage.WriteJSON(path, state); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if _, err := m.Toggle(ctx, c, "ghrawr"); err == nil {
		t.Fatal("accepted mismatched anchored root")
	}
	if len(f.calls) != before+1 {
		t.Fatal("identity mismatch proceeded beyond whoami")
	}
}

func TestCorruptSavedPlacementDoesNotMoveLivePanes(t *testing.T) {
	for _, corruption := range []string{"direction", "ratio", "chain", "siblings", "window"} {
		t.Run(corruption, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			if _, err := m.Open(ctx, c, "ghrawr"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Hide(ctx, c, "ghrawr"); err != nil {
				t.Fatal(err)
			}
			path := statePath(m.StateDir, f.identity, c)
			var state State
			if err := storage.ReadJSON(path, &state); err != nil {
				t.Fatal(err)
			}
			p := state.Hidden["ghrawr"]
			switch corruption {
			case "direction":
				p.Chain[0].Direction = "diagonal"
			case "ratio":
				p.Chain[0].Ratio = 0
			case "chain":
				p.Chain = nil
			case "siblings":
				p.Chain[0].Siblings = nil
			case "window":
				p.WindowID = ""
			}
			state.Hidden["ghrawr"] = p
			if err := storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Open(ctx, c, "ghrawr"); err == nil {
				t.Fatalf("accepted corrupt %s placement", corruption)
			}
			if f.count("session.move_block") != 0 || f.count("session.new_split") != 1 {
				t.Fatalf("corrupt %s state changed live pane geometry", corruption)
			}
		})
	}
}

func TestExternallyReattachedPaneFiltersAndClearsStaleHiddenState(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	opened, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	originalProcess := encode(f.processes[opened.BlockID])
	originalLayout := clean(f.viewSnapshot().Windows[0].Layers[0].Layout)
	if _, err := m.Hide(ctx, c, "ghrawr"); err != nil {
		t.Fatal(err)
	}
	hidden, err := m.Snapshot(ctx, c)
	if err != nil || !reflect.DeepEqual(hidden.Hidden, []string{"ghrawr"}) {
		t.Fatalf("retained pane not reported hidden: %+v %v", hidden, err)
	}
	path := statePath(m.StateDir, f.identity, c)
	var initialState State
	if err := storage.ReadJSON(path, &initialState); err != nil {
		t.Fatal(err)
	}
	beforeStatus, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A native action or Rawr restore reattaches this exact surface without
	// consulting the standalone app's association file.
	if _, err := f.call("session.move_block", map[string]any{"block_id": opened.BlockID, "anchor_block_id": "main", "direction": "horizontal", "side": "after", "ratio": .7}); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Hidden) != 0 || status.Apps["ghrawr"] != opened.BlockID {
		t.Fatalf("visible native pane reported hidden: %+v", status)
	}
	afterStatus, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeStatus, afterStatus) {
		t.Fatal("read-only status changed saved associations")
	}
	creates, moves, detaches := f.count("session.new_split"), f.count("session.move_block"), f.count("session.detach_block")
	focusObserved := false
	m.Rex.Runner = func(ctx context.Context, exe string, args []string) ([]byte, []byte, error) {
		for _, arg := range args {
			if arg == "session.focus_block" {
				var state State
				if err := storage.ReadJSON(path, &state); err != nil {
					t.Fatal(err)
				}
				if _, stale := state.Hidden["ghrawr"]; stale {
					t.Fatal("stale hidden entry was not persisted before native focus")
				}
				focusObserved = true
			}
		}
		return f.run(ctx, exe, args)
	}
	for i := 0; i < 2; i++ {
		reused, err := m.Open(ctx, c, "ghrawr")
		if err != nil || !reused.Reused || !reused.Visible || reused.BlockID != opened.BlockID {
			t.Fatalf("repeat open failed to reuse reattached pane: %+v %v", reused, err)
		}
	}
	var finalState State
	if err := storage.ReadJSON(path, &finalState); err != nil {
		t.Fatal(err)
	}
	if !focusObserved || len(finalState.Hidden) != 0 || len(finalState.Placements) != 0 || !reflect.DeepEqual(initialState.Apps, finalState.Apps) {
		t.Fatalf("repeat open did not clean stale association: %+v", finalState)
	}
	if f.count("session.new_split") != creates || f.count("session.move_block") != moves || f.count("session.detach_block") != detaches {
		t.Fatal("repeat open rearranged or duplicated an already visible surface")
	}
	if !reflect.DeepEqual(originalProcess, encode(f.processes[opened.BlockID])) || !equivalent(originalLayout, f.viewSnapshot().Windows[0].Layers[0].Layout) {
		t.Fatal("repeat open changed native process or layout")
	}
	f.assertNoDestructiveCalls(t)
}
