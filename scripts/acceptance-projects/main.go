// Run after building the four Go commands. Creates only a new named session,
// never writes input into a terminal, and leaves every process running.
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
	"github.com/metcalfc/rawr/internal/projects"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}
func ensure(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func shape(n *rex.LayoutNode) any {
	if n == nil {
		return nil
	}
	if n.BlockID != "" {
		return n.BlockID
	}
	return []any{n.Split.Direction, n.Split.Ratio, shape(n.Split.Before), shape(n.Split.After)}
}
func geometry(v rex.View) any {
	tabs := []any{}
	for _, w := range v.Windows {
		layers := []any{}
		for _, l := range w.Layers {
			layers = append(layers, shape(l.Layout))
		}
		tabs = append(tabs, layers)
	}
	return tabs
}
func pids(ctx context.Context, r *rex.Client, v rex.View) map[string]int {
	result := map[string]int{}
	for bid := range v.Blocks() {
		p, e := r.Process(ctx, v.ID, bid)
		check(e)
		ensure(p.Child != nil, "missing native child")
		result[bid] = p.Child.PID
	}
	return result
}

func recreateExisting() {
	ctx := context.Background()
	cwd, e := os.Getwd()
	check(e)
	path := filepath.Join(cwd, ".acceptance", "rawrkit-projects-location.json")
	var report map[string]any
	check(storage.ReadJSON(path, &report))
	root := report["root"].(string)
	id := report["project_id"].(string)
	bin := filepath.Join(cwd, "build", "bin")
	self, e := os.Executable()
	check(e)
	r := rex.New("", "", "lunchbox")
	m := projects.New(r, filepath.Join(root, "config"), filepath.Join(root, "state"), bin)
	m.AppsDir = filepath.Join(root, "default-state", "rexapps")
	c, e := m.Context(ctx, id)
	check(e)
	pm := catalog.NewManager(r, c.StateDir, bin)
	snapshot, e := pm.Snapshot(ctx, c)
	check(e)
	owned, ok := snapshot.Bindings["ghrawr"]
	ensure(ok, "missing known GH binding")
	process, e := r.Process(ctx, c.Session, owned.BlockID)
	check(e)
	ensure(process.Child != nil && process.Child.PID == owned.PID && process.Child.Path == owned.Path, "GH program changed; refusing fixture close")
	before, e := r.View(ctx, c.Session)
	check(e)
	block, ok := before.Blocks()[owned.BlockID]
	ensure(ok && block.Label == owned.Label, "GH surface changed; refusing fixture close")
	beforePIDs := pids(ctx, r, before)
	// Only the privately created, verified GH fixture is deliberately closed.
	// Every working terminal stays alive; no input is sent to any existing pane.
	check(r.Call(ctx, "block.close", map[string]any{"session_id": c.Session, "block_id": owned.BlockID}, nil))
	marker := filepath.Join(root, "recreated-bare-command-result.json")
	var worker rex.Created
	check(r.Call(ctx, "session.new_window", map[string]any{"session_id": c.Session, "window_label": "App recreation worker", "focus": false, "layout": rex.Leaf("rawr-go-project-recreation-worker", []string{self, "--bare-worker", bin, root, filepath.Join(root, "default-state"), marker, r.Executable}, root)}, &worker))
	ensure(len(worker.BlockIDs) == 1, "ambiguous recreation worker")
	var bare map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for {
		e = storage.ReadJSON(marker, &bare)
		if e == nil {
			break
		}
		ensure(time.Now().Before(deadline), "bare recreation command did not finish")
		time.Sleep(50 * time.Millisecond)
	}
	ensure(bare["plain_command_succeeded"] == true, fmt.Sprint(bare))
	newScope, e := pm.Snapshot(ctx, c)
	check(e)
	replacement := newScope.Apps["ghrawr"]
	ensure(replacement != "" && replacement != owned.BlockID, "bare command did not recreate closed GH")
	current, e := r.View(ctx, c.Session)
	check(e)
	currentPIDs := pids(ctx, r, current)
	for bid, pid := range beforePIDs {
		if bid != owned.BlockID {
			ensure(currentPIDs[bid] == pid, "recreation changed unrelated work")
		}
	}
	ensure(len(current.Blocks()) == len(before.Blocks())+1, "bare recreation created unexpected surfaces")
	restored, e := m.Restore(ctx, id)
	check(e)
	ensure(restored.Result == "partial", "unsaved worker tab should be preserved with partial restore")
	after, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(currentPIDs, pids(ctx, r, after)), "restore duplicated or replaced a native program")
	ensure(reflect.DeepEqual(geometry(current), geometry(after)), "partial restore changed current geometry")
	var runtime map[string]any
	check(storage.ReadJSON(filepath.Join(m.StateDir, id+".json"), &runtime))
	roles := runtime["roles"].(map[string]any)
	ensure(roles["github"].(map[string]any)["block_id"] == replacement, "new shared ID was not persisted")
	checks := report["checks"].(map[string]any)
	checks["closed_app_bare_recreation_rebound_without_duplicate"] = true
	report["recreation_check"] = map[string]any{"old_gh_block_id": owned.BlockID, "new_gh_block_id": replacement, "worker_block_id": worker.BlockIDs[0], "preserved_pids": currentPIDs, "restore": restored}
	check(storage.WriteJSON(filepath.Join(cwd, "docs", "evidence", "rawrkit-projects-native.json"), report))
	check(storage.WriteJSON(path, report))
	fmt.Printf("Native closed-app recreation reused shared binding without duplicates; session %s\n", c.Session)
}

// verifyLegacy inspects only the Go-owned historical acceptance workspace. It
// does not close, swap, detach, relaunch or send input to any of its terminals.
func verifyLegacy() {
	ctx := context.Background()
	cwd, e := os.Getwd()
	check(e)
	var location map[string]any
	check(storage.ReadJSON(filepath.Join(cwd, ".acceptance", "go-projects-location.json"), &location))
	root := location["root"].(string)
	id := location["project_id"].(string)
	sid := location["session_id"].(string)
	r := rex.New("", "", "lunchbox")
	m := projects.New(r, filepath.Join(root, "config"), filepath.Join(root, "state"), filepath.Join(cwd, "build", "bin"))
	m.AppsDir = filepath.Join(root, "default-state", "rexapps")
	definitionPath := filepath.Join(m.ConfigDir, id+".json")
	definitionBefore, e := os.ReadFile(definitionPath)
	check(e)
	p, e := m.Get(id)
	check(e)
	ensure(p.Roles["github"].Kind == "ghrawr" && p.Roles["files"].Kind == "filerawr" && p.Roles["exedev"].Kind == "exerawr", "legacy schema2 recipe names did not normalize")
	before, e := r.View(ctx, sid)
	check(e)
	beforePIDs := pids(ctx, r, before)
	c, e := m.Context(ctx, id)
	check(e)
	snapshot, e := catalog.NewManager(r, c.StateDir, m.BinaryDir).Snapshot(ctx, c)
	check(e)
	programs := map[string]any{}
	for _, kind := range []string{"ghrawr", "filerawr", "exerawr"} {
		binding, ok := snapshot.Bindings[kind]
		ensure(ok, "missing normalized legacy SDK binding")
		process, e := r.Process(ctx, sid, binding.BlockID)
		check(e)
		ensure(process.Child != nil && process.Child.PID == binding.PID && process.Child.Path == binding.Path, "legacy app program changed; preserving without claiming adoption")
		programs[kind] = map[string]any{"block_id": binding.BlockID, "pid": binding.PID, "path": binding.Path}
	}
	planned, e := m.Plan(ctx, id)
	check(e)
	for _, action := range planned.Actions {
		ensure(!strings.HasPrefix(action, "launch "), "legacy live app was treated as missing")
	}
	opened, e := m.Open(ctx, id)
	check(e)
	ensure(!opened.Created && opened.SessionID == sid, "new CLI duplicated legacy Go session")
	after, e := r.View(ctx, sid)
	check(e)
	ensure(reflect.DeepEqual(beforePIDs, pids(ctx, r, after)), "legacy inspection/open changed native child PIDs")
	ensure(reflect.DeepEqual(geometry(before), geometry(after)), "legacy inspection/open changed geometry")
	definitionAfter, e := os.ReadFile(definitionPath)
	check(e)
	ensure(string(definitionBefore) == string(definitionAfter), "read/open rewrote deliberately saved legacy setup")
	report := map[string]any{"session_id": sid, "project_id": id, "programs": programs, "checks": map[string]bool{"schema2_recipe_aliases_normalized_without_saved_write": true, "old_sdk_alias_bindings_canonicalized_in_memory": true, "old_go_executables_reused_with_verified_pids": true, "open_and_plan_preserve_ids_geometry_and_programs": true}, "plan": planned, "ui_limit": "Existing Go app processes keep their original UI code; no hot reload or terminal input was attempted."}
	check(storage.WriteJSON(filepath.Join(cwd, "docs", "evidence", "rawrkit-projects-legacy.json"), report))
	fmt.Printf("Rawrkit compatibility read/open preserved the historical Go workspace %s\n", sid)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--verify-legacy" {
		verifyLegacy()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--recreate-existing" {
		recreateExisting()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--bare-worker" {
		bin, root, state, marker, rexexe := os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6]
		command := exec.Command(filepath.Join(bin, "ghrawr"))
		command.Dir = root
		command.Env = append(os.Environ(), "XDG_STATE_HOME="+state, "REXAPPS_REX="+rexexe)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		err := command.Run()
		value := map[string]any{"plain_command_succeeded": err == nil, "session_id": os.Getenv("REX_SESSION"), "block_id": os.Getenv("REX_BLOCK")}
		if err != nil {
			value["error"] = err.Error()
		}
		check(storage.WriteJSON(marker, value))
		fmt.Println("Project acceptance work: the bare ghrawr command reused the project app; this process stays alive.")
		for {
			time.Sleep(time.Minute)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "--worker" {
		fmt.Println("Project acceptance sentinel: native restore preserves this running process.")
		for {
			time.Sleep(time.Minute)
		}
	}
	ctx := context.Background()
	cwd, e := os.Getwd()
	check(e)
	bin := filepath.Join(cwd, "build", "bin")
	self, e := os.Executable()
	check(e)
	root, e := os.MkdirTemp(filepath.Join(cwd, ".acceptance"), "go-projects-*")
	check(e)
	check(os.WriteFile(filepath.Join(root, "README.md"), []byte("# Rawr Go optional project acceptance\n"), 0600))
	r := rex.New("", "", "lunchbox")
	identity, e := r.Identity(ctx)
	check(e)
	m := projects.New(r, filepath.Join(root, "config"), filepath.Join(root, "state"), bin)
	// Match the actual CLI's common standalone app directory, isolated using a
	// private XDG root so this acceptance never touches the user's preferences.
	defaultState := filepath.Join(root, "default-state")
	m.AppsDir = filepath.Join(defaultState, "rexapps")
	p, e := m.Create("rawr-go-project-acceptance", root, "metcalfc/herdr-exe", "", []string{"filerawr", "ghrawr", "exerawr"})
	check(e)
	opened, e := m.Open(ctx, p.ID)
	check(e)
	ensure(opened.Created && opened.LayoutMatches, "initial B topology mismatch")
	c, e := m.Context(ctx, p.ID)
	check(e)
	report := map[string]any{"rex_version": identity.Version, "project_id": p.ID, "session_id": opened.SessionID, "root": root, "checks": map[string]any{}}
	checks := report["checks"].(map[string]any)
	check(storage.WriteJSON(filepath.Join(cwd, ".acceptance", "rawrkit-projects-location.json"), report))
	before, e := r.View(ctx, c.Session)
	check(e)
	initialPIDs := pids(ctx, r, before)
	twice, e := m.Open(ctx, p.ID)
	check(e)
	ensure(!twice.Created && twice.SessionID == c.Session, "repeat open duplicated project")
	after, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(geometry(before), geometry(after)), "repeat open changed arrangement")
	ensure(reflect.DeepEqual(initialPIDs, pids(ctx, r, after)), "repeat open changed native child")
	checks["open_twice_same_session_blocks_and_pids"] = true
	zero := 0
	saved, e := m.Save(ctx, p.ID, &zero)
	check(e)
	ensure(saved.Revision == 1, "explicit save revision did not advance")
	checks["deliberate_save_revision_1"] = true
	if _, e = m.Save(ctx, p.ID, &zero); e == nil {
		panic("stale expected revision accepted")
	}
	checks["stale_save_refused"] = true
	pm := catalog.NewManager(r, c.StateDir, bin)
	snapshot, e := pm.Snapshot(ctx, c)
	check(e)
	_, e = pm.Hide(ctx, c, "ghrawr")
	check(e)
	hidden, e := r.View(ctx, c.Session)
	check(e)
	again, e := m.Open(ctx, p.ID)
	check(e)
	ensure(!again.Created, "hidden tool made open duplicate session")
	hiddenAgain, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(geometry(hidden), geometry(hiddenAgain)), "open reshaped hidden app")
	checks["open_preserves_hidden_pane_and_current_geometry"] = true
	shown, e := m.Restore(ctx, p.ID)
	check(e)
	ensure(shown.Result == "complete" && shown.LayoutMatches, "restore hidden pane failed")
	showView, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(initialPIDs, pids(ctx, r, showView)), "hidden app restore relaunched work")
	ensure(reflect.DeepEqual(geometry(before), geometry(showView)), "hidden restore geometry differs")
	checks["restore_reuses_hidden_block_and_pid"] = true
	rootSplit := showView.Windows[0].Layers[0].Layout.Split
	ensure(rootSplit != nil, "expected split B")
	check(r.Call(ctx, "session.resize_pane", map[string]any{"session_id": c.Session, "direction": rootSplit.Direction, "before_block_id": rex.Leaves(rootSplit.Before)[0], "after_block_id": rex.Leaves(rootSplit.After)[0], "ratio": .31}, nil))
	resized, e := r.View(ctx, c.Session)
	check(e)
	ensure(!reflect.DeepEqual(geometry(before), geometry(resized)), "native resize did not change geometry")
	restored, e := m.Restore(ctx, p.ID)
	check(e)
	ensure(restored.Result == "complete" && restored.LayoutMatches, "clean native restore did not verify")
	time.Sleep(300 * time.Millisecond)
	final, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(geometry(before), geometry(final)), "clean restore topology/ratios differ")
	ensure(reflect.DeepEqual(initialPIDs, pids(ctx, r, final)), "clean restore changed process IDs")
	checks["resize_restore_exact_topology_ratios_and_same_pids"] = true
	var extra rex.Created
	marker := filepath.Join(root, "bare-command-result.json")
	check(r.Call(ctx, "session.new_split", map[string]any{"session_id": c.Session, "anchor_id": snapshot.MainBlock, "direction": "vertical", "side": "after", "ratio": .7, "focus": false, "layout": rex.Leaf("rawr-go-project-extra-sentinel", []string{self, "--bare-worker", bin, root, defaultState, marker, r.Executable}, root)}, &extra))
	ensure(len(extra.BlockIDs) == 1, "ambiguous sentinel creation")
	var bare map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for {
		e = storage.ReadJSON(marker, &bare)
		if e == nil {
			break
		}
		ensure(time.Now().Before(deadline), "bare project command did not finish")
		time.Sleep(50 * time.Millisecond)
	}
	ensure(bare["plain_command_succeeded"] == true, fmt.Sprint(bare))
	shared, e := pm.Snapshot(ctx, c)
	check(e)
	ensure(reflect.DeepEqual(shared.Apps, snapshot.Apps), "bare command changed project app associations")
	bareView, e := r.View(ctx, c.Session)
	check(e)
	ensure(len(bareView.Blocks()) == len(before.Blocks())+1, "bare command created a duplicate tool")
	checks["bare_ghrawr_inside_project_reuses_shared_scope_and_block"] = true
	extraBefore, e := r.View(ctx, c.Session)
	check(e)
	extraPIDs := pids(ctx, r, extraBefore)
	partial, e := m.Restore(ctx, p.ID)
	check(e)
	ensure(partial.Result == "partial" && !partial.LayoutMatches && len(partial.Conflicts) > 0, "extra work should produce explicit partial restore")
	extraAfter, e := r.View(ctx, c.Session)
	check(e)
	ensure(reflect.DeepEqual(geometry(extraBefore), geometry(extraAfter)), "partial restore changed live work layout")
	ensure(reflect.DeepEqual(extraPIDs, pids(ctx, r, extraAfter)), "partial restore replaced a native program")
	checks["extra_live_program_retained_with_partial_restore"] = true
	one := 1
	extraSaved, e := m.Save(ctx, p.ID, &one)
	check(e)
	ensure(extraSaved.Revision == 2 && len(extraSaved.Notices) > 1, "unknown work not recorded as placeholder")
	definition, e := m.Get(p.ID)
	check(e)
	placeholder := false
	for _, recipe := range definition.Roles {
		if recipe.Kind == "shell" && recipe.Reopen == "shell-placeholder" && len(recipe.Argv) == 0 {
			placeholder = true
		}
	}
	ensure(placeholder, "missing placeholder recipe")
	checks["deliberate_extra_save_uses_shell_placeholder"] = true
	report["native_child_pids"] = extraPIDs
	report["saved_revision"] = extraSaved.Revision
	report["before"] = before
	report["after"] = extraAfter
	report["partial_restore"] = partial
	report["notices"] = extraSaved.Notices
	check(storage.WriteJSON(filepath.Join(cwd, "docs", "evidence", "rawrkit-projects-native.json"), report))
	check(storage.WriteJSON(filepath.Join(cwd, ".acceptance", "rawrkit-projects-location.json"), report))
	data, _ := json.Marshal(map[string]any{"session_id": c.Session, "project_id": p.ID, "config_dir": m.ConfigDir, "state_dir": m.StateDir, "checks": checks})
	fmt.Println(string(data))
}
