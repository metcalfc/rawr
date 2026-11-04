package projects

import (
	"context"
	"testing"
)

func TestRestoreRecreatesExitedResourceAndKeepsWork(t *testing.T) {
	for _, role := range []string{"files", "github", "exedev"} {
		t.Run(role, func(t *testing.T) {
			m, p, f := fixture(t, "filerawr", "ghrawr", "exerawr")
			mustOpen(t, m, p)
			before, err := m.readObserved(p)
			if err != nil {
				t.Fatal(err)
			}
			work := before.Roles["work-main"].BlockID
			workPID := f.process[work].Child.PID
			if f.closeOnExit[work] {
				t.Fatal("project work terminal would close on exit")
			}
			for _, app := range []string{"files", "github", "exedev"} {
				if !f.closeOnExit[before.Roles[app].BlockID] {
					t.Fatalf("%s resource was not hosted with native close-on-exit", app)
				}
			}
			old := before.Roles[role].BlockID
			// Rex removes the completed resource, including it from the inventory.
			f.remove(old)
			delete(f.blocks, old)
			delete(f.process, old)
			calls := len(f.calls)
			r, err := m.Restore(context.Background(), p.ID)
			if err != nil || r.Result != "complete" || !r.LayoutMatches {
				t.Fatal(r, err)
			}
			after, err := m.readObserved(p)
			if err != nil {
				t.Fatal(err)
			}
			fresh := after.Roles[role].BlockID
			if fresh == old || !f.closeOnExit[fresh] || count(f.calls[calls:], "session.new_split") != 1 {
				t.Fatal("restore did not create exactly one auto-closing replacement", f.calls[calls:])
			}
			for other, binding := range before.Roles {
				if other != role && after.Roles[other].BlockID != binding.BlockID {
					t.Fatalf("restore replaced unrelated %s", other)
				}
			}
			if f.process[work].Child.PID != workPID {
				t.Fatal("restore replaced the running work terminal")
			}
			calls = len(f.calls)
			if _, err = m.Restore(context.Background(), p.ID); err != nil || count(f.calls[calls:], "session.new_split") != 0 {
				t.Fatal("repeated restore duplicated the resource", err, f.calls[calls:])
			}
		})
	}
}
