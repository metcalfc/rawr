package rawrkit

import (
	"context"
	"encoding/json"
	"github.com/metcalfc/rawr/rawrkit/rex"
	"reflect"
	"testing"
)

func TestLiveTerminalIdentityCannotSilentlyChangeRecipe(t *testing.T) {
	for _, change := range []string{"argv", "cwd", "metadata", "title"} {
		t.Run(change, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			spec := TerminalSpec{ID: "tasks:watch", Title: "Watch", Argv: []string{"/bin/sleep", "120"}, Cwd: c.Root, Metadata: map[string]string{"task": "watch"}}
			if err := m.ActivateTerminal(ctx, c, spec); err != nil {
				t.Fatal(err)
			}
			before := encode(f.viewSnapshot())
			processes := encode(f.processes)
			changed := spec
			changed.Argv = append([]string(nil), spec.Argv...)
			changed.Metadata = map[string]string{"task": "watch"}
			switch change {
			case "argv":
				changed.Argv[1] = "999"
			case "cwd":
				changed.Cwd = t.TempDir()
			case "metadata":
				changed.Metadata["task"] = "build"
			case "title":
				changed.Title = "Different task"
			}
			if err := m.ActivateTerminal(ctx, c, changed); err == nil {
				t.Fatal("different recipe reused a live terminal")
			}
			if !reflect.DeepEqual(before, encode(f.viewSnapshot())) || !reflect.DeepEqual(processes, encode(f.processes)) || f.count("session.new_window") != 1 {
				t.Fatal("conflicting terminal request changed live work")
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestExplicitReopenAfterViewerExitRetainsEndedSurface(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	spec := TerminalSpec{ID: "file:readme", Title: "README", Argv: []string{"/bin/sleep", "60"}, Cwd: c.Root}
	if err := m.ActivateTerminal(ctx, c, spec); err != nil {
		t.Fatal(err)
	}
	old, err := m.Snapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	f.processes[old.MainBlock] = rex.Process{LastExit: json.RawMessage(`{"exit_code":0}`)}
	if err = m.ActivateTerminal(ctx, c, spec); err != nil {
		t.Fatal(err)
	}
	fresh, err := m.Snapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.MainBlock == old.MainBlock || f.count("session.new_window") != 2 {
		t.Fatal("explicit reopen did not create a fresh view")
	}
	if _, ok := f.viewSnapshot().Blocks()[old.MainBlock]; !ok {
		t.Fatal("ended native surface was discarded")
	}
	f.assertNoDestructiveCalls(t)
}
