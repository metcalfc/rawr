// Native acceptance deliberately creates a separate session. It never sends
// terminal input or closes an existing session. Run after building the four apps.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/internal/storage"
	panes "github.com/metcalfc/rawr/rawrkit"
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
func worker() {
	bin, root, state, marker, rexexe := os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6]
	cmd := exec.Command(filepath.Join(bin, "filerawr"), "--root", root, "--state-dir", state, "--rex", rexexe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	result := map[string]any{"session_id": os.Getenv("REX_SESSION"), "block_id": os.Getenv("REX_BLOCK"), "plain_command_succeeded": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	check(storage.WriteJSON(marker, result))
	fmt.Println("Go acceptance local work: this process stays alive through every native move and swap.")
	for {
		time.Sleep(time.Minute)
	}
}
func pid(ctx context.Context, r *rex.Client, sid, bid string) int {
	p, e := r.Process(ctx, sid, bid)
	check(e)
	ensure(p.Child != nil, "no native child for "+bid)
	return p.Child.PID
}
func tree(n *rex.LayoutNode) any {
	if n == nil {
		return nil
	}
	if n.BlockID != "" {
		return n.BlockID
	}
	return []any{n.Split.Direction, n.Split.Ratio, tree(n.Split.Before), tree(n.Split.After)}
}
func sameTree(a, b *rex.LayoutNode) bool {
	aa, _ := json.Marshal(tree(a))
	bb, _ := json.Marshal(tree(b))
	return string(aa) == string(bb)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--worker" {
		worker()
		return
	}
	ctx := context.Background()
	cwd, e := os.Getwd()
	check(e)
	bin := filepath.Join(cwd, "build", "bin")
	self, e := os.Executable()
	check(e)
	root, e := os.MkdirTemp("", "rexapps-go-acceptance-*")
	check(e)
	root, e = filepath.EvalSymlinks(root)
	check(e)
	for _, args := range [][]string{{"init", "--initial-branch=main", root}, {"-C", root, "remote", "add", "origin", "https://github.com/metcalfc/herdr-exe.git"}} {
		check(exec.Command("git", args...).Run())
	}
	check(os.WriteFile(filepath.Join(root, "README.md"), []byte("# Standalone Go Rex apps\n\nFiles, GitHub and exe.dev are independent native panes.\n"), 0600))
	check(os.Mkdir(filepath.Join(root, "src"), 0700))
	check(os.WriteFile(filepath.Join(root, "src", "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0600))
	state := filepath.Join(root, ".state")
	marker := filepath.Join(root, "worker.json")
	r := rex.New("", "", "lunchbox")
	identity, e := r.Identity(ctx)
	check(e)
	report := map[string]any{"rex_version": identity.Version, "root": root, "state_dir": state, "checks": map[string]any{}}
	checks := report["checks"].(map[string]any)
	var created rex.Created
	check(r.Call(ctx, "session.create", map[string]any{"label": "standalone-rawrs", "initial_windows": []any{map[string]any{"window_label": "Go apps", "layout": rex.Leaf("go-acceptance-local", []string{self, "--worker", bin, root, state, marker, r.Executable}, root)}}}, &created))
	sid := created.SessionID
	ensure(len(created.InitialWindows) == 1 && len(created.InitialWindows[0].BlockIDs) == 1, "ambiguous session creation")
	main := created.InitialWindows[0].BlockIDs[0]
	report["session_id"] = sid
	report["initial_main_block_id"] = main
	check(storage.WriteJSON(filepath.Join(cwd, ".acceptance", "rawrkit-standalone-location.json"), report))
	deadline := time.Now().Add(20 * time.Second)
	var workerResult map[string]any
	for {
		e = storage.ReadJSON(marker, &workerResult)
		if e == nil {
			break
		}
		ensure(time.Now().Before(deadline), "plain command worker did not finish")
		time.Sleep(60 * time.Millisecond)
	}
	ensure(workerResult["plain_command_succeeded"] == true, fmt.Sprint(workerResult))
	ensure(workerResult["session_id"] == sid && workerResult["block_id"] == main, "inherited Rex identity mismatch")
	checks["bare_command_in_rex_returns_to_work"] = true
	h := sha256.Sum256([]byte(root))
	c := model.Context{Root: root, Scope: "root-" + hex.EncodeToString(h[:12]), Session: sid, Anchor: main, StateDir: state, Repository: "metcalfc/herdr-exe", Rex: r.Executable, Client: "lunchbox"}
	m := catalog.NewManager(r, state, bin)
	for _, kind := range []string{"ghrawr", "exerawr"} {
		_, e = m.Open(ctx, c, kind)
		check(e)
	}
	snapshot, e := m.Snapshot(ctx, c)
	check(e)
	pids := map[string]int{"local": pid(ctx, r, sid, main)}
	for kind, bid := range snapshot.Apps {
		pids[kind] = pid(ctx, r, sid, bid)
	}
	before, e := r.View(ctx, sid)
	check(e)
	report["before"] = before
	for _, kind := range []string{"filerawr", "ghrawr", "exerawr"} {
		out, e := m.Open(ctx, c, kind)
		check(e)
		ensure(out.Reused && out.BlockID == snapshot.Apps[kind], "repeat command duplicated app")
	}
	checks["repeat_open_reuses_all_ids"] = true
	for _, kind := range []string{"filerawr", "ghrawr", "exerawr"} {
		out, e := m.Hide(ctx, c, kind)
		check(e)
		ensure(!out.Visible, "hide failed")
	}
	for _, kind := range []string{"ghrawr", "filerawr", "exerawr"} {
		out, e := m.Open(ctx, c, kind)
		check(e)
		ensure(out.Reused && out.BlockID == snapshot.Apps[kind], "show changed app")
	}
	after, e := r.View(ctx, sid)
	check(e)
	ensure(sameTree(before.Windows[0].Layers[0].Layout, after.Windows[0].Layers[0].Layout), "toggle roundtrip changed geometry")
	for kind, bid := range snapshot.Apps {
		ensure(pid(ctx, r, sid, bid) == pids[kind], "toggle changed PID")
	}
	ensure(pid(ctx, r, sid, main) == pids["local"], "local process changed")
	checks["mixed_toggles_same_processes_and_geometry"] = true
	if os.Getenv("REX_ACCEPTANCE_VMS") != "" {
		var targets []string
		check(json.Unmarshal([]byte(os.Getenv("REX_ACCEPTANCE_VMS")), &targets))
		ensure(len(targets) == 2, "provide exactly two existing VM names")
		vm1, e := services.FindVM(ctx, targets[0])
		check(e)
		vm2, e := services.FindVM(ctx, targets[1])
		check(e)
		check(activateVM(ctx, m, c, vm1))
		one, e := m.Snapshot(ctx, c)
		check(e)
		onePID := pid(ctx, r, sid, one.MainBlock)
		check(activateVM(ctx, m, c, vm2))
		two, e := m.Snapshot(ctx, c)
		check(e)
		twoPID := pid(ctx, r, sid, two.MainBlock)
		check(activateVM(ctx, m, c, vm1))
		back, e := m.Snapshot(ctx, c)
		check(e)
		ensure(back.MainBlock == one.MainBlock && pid(ctx, r, sid, back.MainBlock) == onePID, "VM reuse failed")
		ensure(pid(ctx, r, sid, two.MainBlock) == twoPID, "previous VM changed")
		ensure(pid(ctx, r, sid, main) == pids["local"], "previous local work lost")
		_, e = m.LocalTerm(ctx, c)
		check(e)
		local, e := m.Snapshot(ctx, c)
		check(e)
		ensure(local.MainBlock == main && pid(ctx, r, sid, main) == pids["local"], "local work return failed")
		checks["two_real_ssh_shells_reused_and_local_center_restored"] = true
		report["ssh_pids"] = []int{onePID, twoPID}
	}
	// A fourth app imports only the public SDK and UI. No built-in app-name
	// changes are needed to launch, hide and reuse it beside the three rawrs.
	noteExe := filepath.Join(bin, "noterawr")
	check(m.Register(panes.Definition{ID: "noterawr", Title: "Note Rawr", Command: []string{noteExe}, Placement: panes.PlacementSpec{Direction: "horizontal", Side: "after", Ratio: .75}}))
	note, e := m.Open(ctx, c, "noterawr")
	check(e)
	notePID := pid(ctx, r, sid, note.BlockID)

	_, e = m.Hide(ctx, c, "noterawr")
	check(e)
	reused, e := m.Open(ctx, c, "noterawr")
	check(e)
	ensure(reused.Reused && reused.BlockID == note.BlockID && pid(ctx, r, sid, note.BlockID) == notePID, "independent SDK app not reused")
	_, e = m.Hide(ctx, c, "noterawr")
	check(e)
	checks["independent_public_sdk_ui_app_native_lifecycle"] = true
	report["sdk_example_block_id"] = note.BlockID
	report["sdk_example_pid"] = notePID
	final, e := r.View(ctx, sid)
	check(e)
	report["final"] = final
	report["tool_pids"] = pids
	check(r.Select(ctx, sid, main))
	check(storage.WriteJSON(filepath.Join(cwd, "docs", "evidence", "rawrkit-panes-native.json"), report))
	check(storage.WriteJSON(filepath.Join(cwd, ".acceptance", "rawrkit-standalone-location.json"), report))
	fmt.Printf("Native Go acceptance passed; session %s, root %s\n", sid, root)
}

func activateVM(ctx context.Context, m *panes.Manager, c model.Context, vm model.VM) error {
	spec, err := catalog.Terminal(vm, c.Root)
	if err != nil {
		return err
	}
	return m.ActivateTerminal(ctx, c, spec)
}
