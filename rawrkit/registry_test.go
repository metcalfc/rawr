package rawrkit

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnrelatedRegisteredAppUsesSameNativeLifecycle(t *testing.T) {
	m, c, f := newFakeManager(t)
	path := filepath.Join(m.BinaryDir, "noterawr")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(Definition{ID: "noterawr", Title: "Notes", Aliases: []string{"notes"}, Command: []string{path, "--literal", "$(not a shell)"}, Placement: PlacementSpec{Direction: "vertical", Side: "before", Ratio: .3}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := m.Open(ctx, c, "notes")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Open(ctx, c, "noterawr")
	if err != nil || first.BlockID != second.BlockID || !second.Reused {
		t.Fatal(first, second, err)
	}
	pid := f.processes[first.BlockID].Child.PID
	if _, err = m.Hide(ctx, c, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Open(ctx, c, "noterawr"); err != nil {
		t.Fatal(err)
	}
	if f.processes[first.BlockID].Child.PID != pid || f.count("session.new_split") != 1 {
		t.Fatal("custom app recreated")
	}
	f.assertNoDestructiveCalls(t)
}

func TestDefinitionValidationBeforeAnyNativeMutation(t *testing.T) {
	for _, def := range []Definition{{ID: "bad id"}, {ID: "other", Aliases: []string{"ghrawr"}}, {ID: "other", Placement: PlacementSpec{Ratio: 2}}, {ID: "other", Command: []string{"bad\x00exe"}}} {
		m, _, f := newFakeManager(t)
		before := append([]string(nil), f.calls...)
		if err := m.Register(def); err == nil {
			t.Fatal("invalid definition accepted", def)
		}
		if !reflect.DeepEqual(before, f.calls) {
			t.Fatal("definition validation contacted Rex")
		}
	}
}

func TestUnknownAppStateIsPreservedAcrossIndependentRegistries(t *testing.T) {
	m, c, _ := newFakeManager(t)
	ctx := context.Background()
	out, err := m.Open(ctx, c, "ghrawr")
	if err != nil {
		t.Fatal(err)
	}
	other := New(m.Rex, m.StateDir, m.BinaryDir, Definition{ID: "other-app"})
	s, err := other.Snapshot(ctx, c)
	if err != nil || s.Apps["ghrawr"] != out.BlockID {
		t.Fatal(s, err)
	}
	if _, err = other.Open(ctx, c, "ghrawr"); err == nil {
		t.Fatal("unregistered app launched")
	}
}
