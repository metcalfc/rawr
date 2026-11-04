package rawrkit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

// Native exit removes a surface, whereas hide only detaches it. These tests
// simulate that external event without asking the SDK to close any terminal.
func (f *fakeNative) appExited(id string) {
	f.detach(id)
	delete(f.blocks, id)
	delete(f.processes, id)
	f.view.Revision++
}

func fakeRequest(t *testing.T, argv []string) (string, map[string]any) {
	t.Helper()
	for j, arg := range argv {
		if arg == "api" && j+3 < len(argv) && argv[j+1] == "call" {
			var args map[string]any
			if err := json.Unmarshal([]byte(argv[j+3]), &args); err != nil {
				t.Fatal(err)
			}
			return argv[j+2], args
		}
	}
	return "", nil
}

func TestExitedVisibleAppReopensExactlyOnceAndRetainsOtherWork(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	base := m.Rex.Runner
	appLeaves := 0
	m.Rex.Runner = func(ctx context.Context, exe string, args []string) ([]byte, []byte, error) {
		method, request := fakeRequest(t, args)
		if method == "session.new_split" {
			options := request["layout"].(map[string]any)["block"].(map[string]any)["options"].(map[string]any)
			if options["exit"].(map[string]any)["on_completion"] != true {
				t.Fatal("resource app did not request native close on completion")
			}
			appLeaves++
		}
		return base(ctx, exe, args)
	}
	opened := map[string]Result{}
	for _, kind := range []string{"filerawr", "ghrawr", "exerawr"} {
		result, err := m.Open(ctx, c, kind)
		if err != nil {
			t.Fatal(err)
		}
		opened[kind] = result
	}
	main := encode(f.processes["main"])
	files := encode(f.processes[opened["filerawr"].BlockID])
	vms := encode(f.processes[opened["exerawr"].BlockID])
	f.appExited(opened["ghrawr"].BlockID)
	fresh, err := m.Open(ctx, c, "ghrawr")
	if err != nil || fresh.Reused || fresh.BlockID == opened["ghrawr"].BlockID {
		t.Fatalf("fresh app: %+v %v", fresh, err)
	}
	again, err := m.Open(ctx, c, "ghrawr")
	if err != nil || !again.Reused || again.BlockID != fresh.BlockID || appLeaves != 4 {
		t.Fatalf("repeat open duplicated app: %+v %v, leaves=%d", again, err, appLeaves)
	}
	if !reflect.DeepEqual(main, encode(f.processes["main"])) || !reflect.DeepEqual(files, encode(f.processes[opened["filerawr"].BlockID])) || !reflect.DeepEqual(vms, encode(f.processes[opened["exerawr"].BlockID])) {
		t.Fatal("reopening an exited app changed other live work")
	}
	f.assertNoDestructiveCalls(t)
}

func TestExitedHiddenAppDoesNotDiscardHiddenSibling(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	files, err := m.Open(ctx, c, "filerawr")
	if err != nil {
		t.Fatal(err)
	}
	gh, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ghrawr", "filerawr"} {
		if _, err = m.Hide(ctx, c, kind); err != nil {
			t.Fatal(err)
		}
	}
	main, fileProcess := encode(f.processes["main"]), encode(f.processes[files.BlockID])
	f.appExited(gh.BlockID)
	status, err := m.Status(ctx, c)
	if err != nil || !reflect.DeepEqual(status.Hidden, []string{"filerawr"}) {
		t.Fatalf("exited app still reported hidden: %+v %v", status, err)
	}
	shown, err := m.Open(ctx, c, "filerawr")
	if err != nil || !shown.Reused || shown.BlockID != files.BlockID {
		t.Fatalf("hidden sibling was not retained: %+v %v", shown, err)
	}
	fresh, err := m.Open(ctx, c, "ghrawr")
	if err != nil || fresh.Reused || fresh.BlockID == gh.BlockID {
		t.Fatalf("hidden exited app was not recreated: %+v %v", fresh, err)
	}
	again, err := m.Open(ctx, c, "ghrawr")
	if err != nil || !again.Reused || again.BlockID != fresh.BlockID || f.count("session.new_split") != 3 {
		t.Fatal("repeated open duplicated the recreated hidden app", err)
	}
	if !reflect.DeepEqual(main, encode(f.processes["main"])) || !reflect.DeepEqual(fileProcess, encode(f.processes[files.BlockID])) {
		t.Fatal("hidden sibling or main program changed")
	}
	var state State
	if err = storage.ReadJSON(statePath(m.StateDir, f.identity, c), &state); err != nil || len(state.Hidden) != 0 || len(state.Placements) != 0 {
		t.Fatalf("stale hidden state remained: %+v %v", state, err)
	}
	f.assertNoDestructiveCalls(t)
}

func TestNextExplicitOpenReconcilesAppExitDuringNativeOperation(t *testing.T) {
	for _, operation := range []string{"open", "reopen hidden", "hide", "show"} {
		t.Run(operation, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			var app Result
			var err error
			if operation != "open" {
				app, err = m.Open(ctx, c, "ghrawr")
				if err != nil {
					t.Fatal(err)
				}
			}
			if operation == "show" || operation == "reopen hidden" {
				if _, err = m.Hide(ctx, c, "ghrawr"); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "reopen hidden" {
				f.appExited(app.BlockID)
			}
			main := encode(f.processes["main"])
			base := m.Rex.Runner
			exited := false
			m.Rex.Runner = func(ctx context.Context, exe string, args []string) ([]byte, []byte, error) {
				method, request := fakeRequest(t, args)
				if !exited && (operation == "open" || operation == "reopen hidden") && method == "session.new_split" {
					out, stderr, err := base(ctx, exe, args)
					var created rex.Created
					if err != nil || json.Unmarshal(out, &created) != nil || len(created.BlockIDs) != 1 {
						t.Fatal("invalid fake creation", err)
					}
					app.BlockID = created.BlockIDs[0]
					f.appExited(app.BlockID)
					exited = true
					return out, stderr, nil
				}
				if !exited && ((operation == "hide" && method == "session.detach_block") || (operation == "show" && method == "session.move_block")) && request["block_id"] == app.BlockID {
					f.calls = append(f.calls, method)
					f.appExited(app.BlockID)
					exited = true
					return nil, nil, errors.New("app exited before native operation")
				}
				return base(ctx, exe, args)
			}
			if operation == "hide" {
				_, err = m.Hide(ctx, c, "ghrawr")
			} else {
				_, err = m.Open(ctx, c, "ghrawr")
			}
			if err == nil || !exited {
				t.Fatal("fake exit did not interrupt the operation")
			}
			m.Rex.Runner = base
			path := statePath(m.StateDir, f.identity, c)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			status, err := m.Status(ctx, c)
			if err != nil || status.Pending == nil || status.Pending.BlockID != app.BlockID {
				t.Fatalf("known returned ID was not journaled: %+v %v", status, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("read-only status reconciled or changed the journal", err)
			}
			creates := f.count("session.new_split")
			fresh, err := m.Open(ctx, c, "ghrawr")
			if err != nil || fresh.Reused || fresh.BlockID == app.BlockID {
				t.Fatalf("next explicit open could not recover: %+v %v", fresh, err)
			}
			again, err := m.Open(ctx, c, "ghrawr")
			if err != nil || !again.Reused || again.BlockID != fresh.BlockID || f.count("session.new_split") != creates+1 {
				t.Fatal("recovery retried or duplicated creation", err)
			}
			if !reflect.DeepEqual(main, encode(f.processes["main"])) {
				t.Fatal("recovery changed the original program")
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestAppJournalRecoveryRefusesUncertainOrOtherWork(t *testing.T) {
	for _, condition := range []string{"unknown creation ID", "unregistered app", "unknown operation", "live app", "changed live app", "main", "local", "managed terminal"} {
		t.Run(condition, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			app, err := m.Open(ctx, c, "ghrawr")
			if err != nil {
				t.Fatal(err)
			}
			path := statePath(m.StateDir, f.identity, c)
			var state State
			if err = storage.ReadJSON(path, &state); err != nil {
				t.Fatal(err)
			}
			pending := &Pending{Operation: "hide", BlockID: app.BlockID}
			switch condition {
			case "unknown creation ID":
				pending = &Pending{Operation: "open ghrawr", Label: "rexapp:ghrawr:unknown-reply"}
			case "unregistered app":
				delete(m.definitions, "ghrawr")
			case "unknown operation":
				pending.Operation = "activate terminal"
			case "main":
				pending = &Pending{Operation: "open ghrawr", BlockID: "main", Label: "rexapp:ghrawr:main"}
			case "local", "managed terminal":
				spec := TerminalSpec{ID: "task:managed", Title: "Managed", Argv: []string{"/bin/sleep", "60"}, Cwd: c.Root}
				if err = m.ActivateTerminal(ctx, c, spec); err != nil {
					t.Fatal(err)
				}
				managed, err := m.Snapshot(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				if condition == "managed terminal" {
					if _, err = m.LocalTerm(ctx, c); err != nil {
						t.Fatal(err)
					}
				}
				if err = storage.ReadJSON(path, &state); err != nil {
					t.Fatal(err)
				}
				pending = &Pending{Operation: "open ghrawr", BlockID: managed.MainBlock, Label: "rexapp:ghrawr:managed"}
				if condition == "local" {
					pending.BlockID = state.LocalBlock
				}
			}
			state.Pending = pending
			if err = storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			if condition == "changed live app" {
				f.processes[app.BlockID].Child.PID++
			} else if condition != "live app" {
				if pending.BlockID != "" {
					f.appExited(pending.BlockID)
				} else {
					f.appExited(app.BlockID)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			creates, moves, detaches := f.count("session.new_split"), f.count("session.move_block"), f.count("session.detach_block")
			if _, err = m.Open(ctx, c, "filerawr"); err == nil || !strings.Contains(err.Error(), "interrupted") {
				t.Fatalf("uncertain journal was accepted: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) || f.count("session.new_split") != creates || f.count("session.move_block") != moves || f.count("session.detach_block") != detaches {
				t.Fatal("refused recovery changed journal or native work", err)
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestShowExitRecoveryPreservesPartialOrChangedSurvivors(t *testing.T) {
	for _, condition := range []string{"legacy", "incomplete", "detached", "label", "host", "child", "foreground", "opaque"} {
		t.Run(condition, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			app, err := m.Open(ctx, c, "ghrawr")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Hide(ctx, c, "ghrawr"); err != nil {
				t.Fatal(err)
			}
			path := statePath(m.StateDir, f.identity, c)
			var state State
			if err = storage.ReadJSON(path, &state); err != nil {
				t.Fatal(err)
			}
			if condition == "opaque" {
				f.processes["main"] = rex.Process{}
			}
			pending := &Pending{Operation: "show", BlockID: app.BlockID, Blocks: []string{"main", app.BlockID}, Witnesses: map[string]PendingWitness{}}
			for _, bid := range pending.Blocks {
				pending.Witnesses[bid] = pendingWitness(f.blocks[bid], f.processes[bid])
			}
			switch condition {
			case "legacy":
				pending.Witnesses = nil
			case "incomplete":
				delete(pending.Witnesses, "main")
			case "detached":
				f.detach("main")
			case "label":
				block := f.blocks["main"]
				block.Label = "different work"
				f.blocks["main"] = block
			case "host":
				block := f.blocks["main"]
				block.HostID = "different host"
				f.blocks["main"] = block
			case "child":
				f.processes["main"].Child.PID++
			case "foreground":
				f.processes["main"].Foreground.PID++
			}
			state.Pending = pending
			if err = storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			f.appExited(app.BlockID)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			creates, moves, detaches := f.count("session.new_split"), f.count("session.move_block"), f.count("session.detach_block")
			if _, err = m.Open(ctx, c, "ghrawr"); err == nil || !strings.Contains(err.Error(), "interrupted") {
				t.Fatalf("partial/changed show journal accepted: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(before, after) || f.count("session.new_split") != creates || f.count("session.move_block") != moves || f.count("session.detach_block") != detaches {
				t.Fatal("refused show recovery changed native work or journal", err)
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}
