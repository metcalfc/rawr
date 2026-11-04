// Exercises Go persistence against one private Rex server. The normal server is
// never targeted; only this script's own child server may be stopped.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

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
func childPIDs(ctx context.Context, r *rex.Client, v rex.View) map[string]int {
	ids := map[string]int{}
	for bid := range v.Blocks() {
		proc, e := r.Process(ctx, v.ID, bid)
		check(e)
		ensure(proc.Child != nil, "native process missing")
		ids[bid] = proc.Child.PID
	}
	return ids
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--worker" {
		fmt.Println("Private restart sentinel: this known program is deliberately not relaunched after restart.")
		for {
			time.Sleep(time.Minute)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cwd, e := os.Getwd()
	check(e)
	self, e := os.Executable()
	check(e)
	base, e := os.MkdirTemp("/tmp", "rawr-go-restart-*")
	check(e)
	config := filepath.Join(base, "init.lua")
	check(os.WriteFile(config, []byte(""), 0600))
	shell := filepath.Join(base, "shell")
	check(os.Mkdir(shell, 0700))
	check(os.WriteFile(filepath.Join(shell, ".zshrc"), []byte("PROMPT='restart-fixture% '\n"), 0600))
	log, e := os.OpenFile(filepath.Join(base, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	check(e)
	defer log.Close()
	r := rex.New("", "unix://"+filepath.Join(base, "server.sock"), "")
	var server *exec.Cmd
	var ended chan error
	start := func() {
		server = exec.Command(r.Executable, "server", "run", "--listen", r.Server, "--state-dir", filepath.Join(base, "rex-state"), "--config", config)
		server.Env = append(os.Environ(), "ZDOTDIR="+shell)
		server.Stdout = log
		server.Stderr = log
		check(server.Start())
		ended = make(chan error, 1)
		go func(c *exec.Cmd, done chan error) { done <- c.Wait() }(server, ended)
	}
	stop := func() {
		check(r.Call(ctx, "server.stop", map[string]any{}, nil))
		select {
		case err := <-ended:
			check(err)
		case <-time.After(10 * time.Second):
			panic("private server did not stop")
		}
		server = nil
	}
	defer func() {
		if server != nil {
			_ = server.Process.Kill()
			select {
			case <-ended:
			case <-time.After(2 * time.Second):
			}
		}
	}()
	wait := func(previous string) rex.Identity {
		deadline := time.Now().Add(12 * time.Second)
		for {
			identity, err := r.Identity(ctx)
			if err == nil && identity.InstanceID != previous {
				return identity
			}
			ensure(time.Now().Before(deadline), "private Rex startup failed; inspect "+filepath.Join(base, "server.log"))
			time.Sleep(80 * time.Millisecond)
		}
	}
	start()
	firstIdentity := wait("")
	root := filepath.Join(base, "project 雪 ; literal")
	check(os.Mkdir(root, 0700))
	check(os.WriteFile(filepath.Join(root, "README.md"), []byte("# Private Go restart fixture\n"), 0600))
	m := projects.New(r, filepath.Join(base, "projects"), filepath.Join(base, "runtime"), filepath.Join(cwd, "build", "bin"))
	p, e := m.Create("private-go-restart", root, "metcalfc/herdr-exe", "", []string{"filerawr", "ghrawr", "exerawr"})
	check(e)
	// This is a fixture for the saved VM-shell recipe policy, with no SSH or
	// external VM access. Its exact executable is known before native creation.
	p.Roles["work-main"] = projects.Recipe{Kind: "vm-shell", Argv: []string{self, "--worker"}, Cwd: root, Host: "local", Reopen: "explicit"}
	check(storage.WriteJSON(filepath.Join(m.ConfigDir, p.ID+".json"), p))
	opened, e := m.Open(ctx, p.ID)
	check(e)
	ensure(opened.Created, "private session not created")
	_, e = m.Save(ctx, p.ID, nil)
	check(e)
	before, e := r.View(ctx, opened.SessionID)
	check(e)
	oldPIDs := childPIDs(ctx, r, before)
	report := map[string]any{"rex_version": firstIdentity.Version, "private_endpoint": r.Server, "project_id": p.ID, "session_id": opened.SessionID, "checks": map[string]any{}, "old_pids": oldPIDs}
	checks := report["checks"].(map[string]any)
	stop()
	start()
	secondIdentity := wait(firstIdentity.InstanceID)
	ensure(secondIdentity.HostID == firstIdentity.HostID, "private host changed")
	checks["host_same_generation_changed"] = true
	after, e := r.View(ctx, opened.SessionID)
	check(e)
	newPIDs := childPIDs(ctx, r, after)
	ensure(len(newPIDs) == len(oldPIDs), "restart changed native surface count")
	for bid, old := range oldPIDs {
		fresh, ok := newPIDs[bid]
		ensure(ok && fresh != old, "restart failed to retain ID with fresh process")
	}
	checks["surface_ids_retained_with_fresh_shell_pids"] = true
	main := before.Focused()
	ensure(main != "", "missing main focus")
	var capturedBefore string
	capture := func() string {
		var result []byte
		command := exec.CommandContext(ctx, r.Executable, "--autostart=false", "-S", r.Server, "capture", "-s", opened.SessionID, "-b", main, "--trim", "--unwrap")
		result, e = command.Output()
		check(e)
		return string(result)
	}
	// The private shell has no user startup hooks, so this is a stable read-only
	// transcript comparison; no `send` API is used anywhere in this script.
	time.Sleep(200 * time.Millisecond)
	capturedBefore = capture()
	again, e := m.Open(ctx, p.ID)
	check(e)
	ensure(!again.Created && again.SessionID == opened.SessionID, "open recreated restarted session")
	planned, e := m.Plan(ctx, p.ID)
	check(e)
	ensure(planned.Result == "partial" && len(planned.Conflicts) > 0, "restart plan did not report fresh-shell conflict")
	restored, e := m.Restore(ctx, p.ID)
	check(e)
	ensure(restored.Result == "partial", "restart restore should preserve fresh shells")
	preserved, e := r.View(ctx, opened.SessionID)
	check(e)
	ensure(reflect.DeepEqual(newPIDs, childPIDs(ctx, r, preserved)), "restore replaced a fresh shell")
	ensure(capturedBefore == capture(), "Open/Plan/Restore wrote unexpected terminal text")
	ensure(len(preserved.Blocks()) == len(before.Blocks()), "restart operations duplicated tools")
	checks["open_plan_restore_no_launch_no_terminal_input"] = true
	saved, e := m.Save(ctx, p.ID, nil)
	check(e)
	definition, e := m.Get(p.ID)
	check(e)
	ensure(definition.Roles["work-main"].Kind == "shell" && len(definition.Roles["work-main"].Argv) == 0, "stale vm-shell recipe was inferred as a live SSH shell")
	ensure(len(saved.Notices) >= 4, "fresh app shell placeholders were not reported")
	checks["save_after_restart_drops_stale_vm_recipe_to_shell_placeholder"] = true
	ensure(!strings.Contains(capture(), "Private restart sentinel"), "Rex unexpectedly restored the old program output")
	checks["prior_program_output_not_restored"] = true
	report["new_pids"] = newPIDs
	report["restart_plan"] = planned
	report["restart_restore"] = restored
	report["save_notices"] = saved.Notices
	stop()
	check(storage.WriteJSON(filepath.Join(cwd, "docs", "evidence", "rawrkit-restart-native.json"), report))
	fmt.Printf("Private Go Rex restart acceptance passed: %v\n", checks)
}
