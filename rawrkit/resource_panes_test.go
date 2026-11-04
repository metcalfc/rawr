package rawrkit

import (
	"context"
	"github.com/metcalfc/rawr/rawrkit/rex"
	"testing"
)

func TestResourcePaneOrderKeepsIndependentNativeLayout(t *testing.T) {
	orders := [][]string{{"filerawr", "ghrawr", "exerawr"}, {"filerawr", "exerawr", "ghrawr"}, {"ghrawr", "filerawr", "exerawr"}, {"ghrawr", "exerawr", "filerawr"}, {"exerawr", "ghrawr", "filerawr"}, {"exerawr", "filerawr", "ghrawr"}}
	want := `[["work",["horizontal",0.2,"@filerawr",["horizontal",0.7,"@main",["vertical",0.5,"@ghrawr","@exerawr"]]]]]`
	for _, order := range orders {
		t.Run(order[0]+"-"+order[1], func(t *testing.T) {
			m, c, f := newFakeManager(t)
			ctx := context.Background()
			for _, app := range order {
				if _, err := m.Open(ctx, c, app); err != nil {
					t.Fatal(err)
				}
			}
			err := m.withState(ctx, c, false, func(s *State, _ rex.Identity, v rex.View, _ string) error {
				if got := shape(s, v); got != want {
					t.Errorf("layout: %s", got)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestResourceWorkReusesCenterAndPreservesLocalProcess(t *testing.T) {
	m, c, f := newFakeManager(t)
	ctx := context.Background()
	for _, app := range []string{"filerawr", "ghrawr", "exerawr"} {
		if _, err := m.Open(ctx, c, app); err != nil {
			t.Fatal(err)
		}
	}
	local := f.processes["main"]
	var before string
	m.withState(ctx, c, false, func(s *State, _ rex.Identity, v rex.View, _ string) error { before = shape(s, v); return nil })
	spec := TerminalSpec{ID: "file:readme", Title: "File README", Argv: []string{"/bin/sleep", "60"}, Cwd: c.Root}
	if err := m.ActivateTerminal(ctx, c, spec); err != nil {
		t.Fatal(err)
	}
	first, err := m.Snapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	pid := f.processes[first.MainBlock].Child.PID
	if err = m.ActivateTerminal(ctx, c, spec); err != nil {
		t.Fatal(err)
	}
	if f.count("session.new_window") != 1 || f.processes[first.MainBlock].Child.PID != pid {
		t.Fatal("reopened resource duplicated its live view")
	}
	if f.viewSnapshot().Focused() != first.MainBlock {
		t.Fatal("resource action did not focus its work")
	}
	if f.processes["main"].Child.PID != local.Child.PID || f.processes["main"].Foreground.PID != local.Foreground.PID {
		t.Fatal("resource action replaced live local work")
	}
	m.withState(ctx, c, false, func(s *State, _ rex.Identity, v rex.View, _ string) error {
		if shape(s, v) != before {
			t.Error("resource swap resized or rearranged panes")
		}
		return nil
	})
	if _, err = m.LocalTerm(ctx, c); err != nil {
		t.Fatal(err)
	}
	if f.viewSnapshot().Focused() != "main" {
		t.Fatal("local return did not focus original work")
	}
	f.assertNoDestructiveCalls(t)
}
