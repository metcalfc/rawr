package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func legacyFixture(t *testing.T) (rawrkit.State, model.VM, rawrkit.Binding) {
	t.Helper()
	t.Setenv("EXE_SSH_KEY", "")
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("SSH executable required for native terminal recipe equivalence")
	}
	vm := model.VM{Name: "saved-vm", Status: "running", SSHDest: "vm+saved-vm@relay.example"}
	binding := rawrkit.Binding{BlockID: "block:retained", Label: "retained ssh", PID: 1234, Path: ssh}
	raw, err := json.Marshal(struct {
		VM      model.VM        `json:"vm"`
		Binding rawrkit.Binding `json:"binding"`
	}{vm, binding})
	if err != nil {
		t.Fatal(err)
	}
	return rawrkit.State{Context: rawrkit.Context{Root: t.TempDir()}, LegacyVMs: map[string]json.RawMessage{vm.Name: raw}, LegacySelected: vm.Name, Terminals: map[string]rawrkit.TerminalEntry{}}, vm, binding
}

func TestLegacyVMConversionPreservesRetainedProcessAndSelection(t *testing.T) {
	state, vm, binding := legacyFixture(t)
	if err := migrateLegacy(&state); err != nil {
		t.Fatal(err)
	}
	entry := state.Terminals["exe:"+vm.Name]
	if entry.Binding != binding || state.Selected != "exe:"+vm.Name || state.LegacyVMs != nil || state.LegacySelected != "" {
		t.Fatalf("legacy conversion changed process identity: %+v", state)
	}
	if len(entry.Spec.Argv) == 0 || entry.Spec.Argv[len(entry.Spec.Argv)-1] != vm.SSHDest || entry.Spec.Metadata["provider"] != "exe.dev" {
		t.Fatalf("legacy terminal recipe: %+v", entry.Spec)
	}
}

func TestEquivalentDualVMMetadataAllowsResolvedExecutableSpelling(t *testing.T) {
	state, vm, binding := legacyFixture(t)
	spec, err := Terminal(vm, state.Context.Root)
	if err != nil {
		t.Fatal(err)
	}
	// ActivateTerminal resolves argv[0] before persisting it. A legacy VM record
	// must compare equivalent to this real new-format representation.
	spec.Argv[0] = binding.Path
	state.Terminals[spec.ID] = rawrkit.TerminalEntry{Spec: spec, Binding: binding}
	if err := migrateLegacy(&state); err != nil {
		t.Fatalf("equivalent dual metadata rejected: %v", err)
	}
	if got := state.Terminals[spec.ID]; got.Binding != binding || !reflect.DeepEqual(got.Spec, spec) {
		t.Fatalf("conversion altered existing terminal: %+v", got)
	}
}

func TestConflictingDualVMMetadataPreservesFailure(t *testing.T) {
	for _, conflict := range []string{"pid", "block", "argv", "cwd", "metadata", "selected"} {
		t.Run(conflict, func(t *testing.T) {
			state, vm, binding := legacyFixture(t)
			spec, err := Terminal(vm, state.Context.Root)
			if err != nil {
				t.Fatal(err)
			}
			entry := rawrkit.TerminalEntry{Spec: spec, Binding: binding}
			switch conflict {
			case "pid":
				entry.Binding.PID++
			case "block":
				entry.Binding.BlockID = "another retained process"
			case "argv":
				entry.Spec.Argv = append(entry.Spec.Argv, "unexpected remote command")
			case "cwd":
				entry.Spec.Cwd = t.TempDir()
			case "metadata":
				entry.Spec.Metadata["provider"] = "another provider"
			case "selected":
				state.Selected = "task:other"
			}
			state.Terminals[spec.ID] = entry
			if err := migrateLegacy(&state); err == nil {
				t.Fatalf("accepted conflicting %s metadata", conflict)
			}
		})
	}
}

func TestLegacyAliasCollisionsRejectBeforeNativeMutation(t *testing.T) {
	for _, collision := range []string{"bindings", "placements"} {
		t.Run(collision, func(t *testing.T) {
			root := t.TempDir()
			dir := t.TempDir()
			c := rawrkit.Context{Root: root, Session: "session", Scope: "scope", Anchor: "main"}
			identity := rex.Identity{HostID: "host", InstanceID: "generation", Version: rex.TestedVersion, Endpoint: "unix:/tmp/test"}
			binding := rawrkit.Binding{BlockID: "old", Label: "old", PID: 12, Path: filepath.Join(root, "ghrex")}
			state := rawrkit.State{Schema: 1, Context: c, Host: identity.HostID, Generation: identity.InstanceID, MainBlock: "main", LocalBlock: "main", Apps: map[string]rawrkit.Binding{"ghrex": binding}, Terminals: map[string]rawrkit.TerminalEntry{}, Hidden: map[string]rawrkit.Placement{}, Placements: map[string]rawrkit.Placement{}}
			if collision == "bindings" {
				other := binding
				other.BlockID = "new"
				other.PID++
				state.Apps["ghrawr"] = other
			} else {
				placement := rawrkit.Placement{WindowID: "work", Chain: []rawrkit.Step{{Direction: "horizontal", Side: "after", Ratio: .7, Siblings: []string{"@main"}}}}
				state.Hidden["ghrex"], state.Hidden["ghrawr"] = placement, placement
			}
			key := sha256.Sum256([]byte(identity.HostID + "\x00" + c.Session + "\x00" + c.Scope))
			path := filepath.Join(dir, hex.EncodeToString(key[:16])+".json")
			if err := storage.WriteJSON(path, state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			client := rex.New("rex", "", "")
			calls := 0
			client.Runner = func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
				calls++
				if !strings.Contains(strings.Join(args, " "), "whoami") {
					t.Fatalf("alias collision progressed to native operation: %q", args)
				}
				data, _ := json.Marshal(identity)
				return data, nil, nil
			}
			manager := NewManager(client, dir, root)
			if _, err := manager.Snapshot(context.Background(), c); err == nil {
				t.Fatalf("accepted alias %s collision", collision)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !reflect.DeepEqual(before, after) {
				t.Fatal("alias collision rewrote state or changed native work")
			}
		})
	}
}
