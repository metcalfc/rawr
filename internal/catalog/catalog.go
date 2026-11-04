// Package catalog defines the built-in rawrs. Rawrkit itself knows no app names
// or provider models; independent apps register their own definitions.
package catalog

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func Definitions() []rawrkit.Definition {
	return []rawrkit.Definition{
		{ID: "filerawr", Title: "File Rawr", Aliases: []string{"filerex"}, Placement: rawrkit.PlacementSpec{Direction: "horizontal", Side: "before", Ratio: .2, WholeLayer: true}},
		{ID: "ghrawr", Title: "GitHub Rawr", Aliases: []string{"ghrex"}, Placement: rawrkit.PlacementSpec{Direction: "horizontal", Side: "after", Ratio: .7}},
		{ID: "exerawr", Title: "Exe Rawr", Aliases: []string{"exerex"}, Placement: rawrkit.PlacementSpec{Direction: "vertical", Side: "after", Ratio: .5, AnchorApp: "ghrawr"}},
	}
}

func Canonical(id string) string {
	for _, d := range Definitions() {
		if id == d.ID {
			return d.ID
		}
		for _, alias := range d.Aliases {
			if alias == id {
				return d.ID
			}
		}
	}
	return id
}

func NewManager(client *rex.Client, stateDir, binaryDir string) *rawrkit.Manager {
	m := rawrkit.New(client, stateDir, binaryDir, Definitions()...)
	m.MigrateState = migrateLegacy
	return m
}

func Terminal(vm model.VM, cwd string) (rawrkit.TerminalSpec, error) {
	argv, err := services.SSHArgs(vm)
	if err != nil {
		return rawrkit.TerminalSpec{}, err
	}
	return rawrkit.TerminalSpec{ID: "exe:" + vm.Name, Title: "VM · " + vm.Name, Argv: argv, Cwd: cwd, Metadata: map[string]string{"provider": "exe.dev", "name": vm.Name, "ssh_dest": vm.SSHDest}}, nil
}

func migrateLegacy(s *rawrkit.State) error {
	if s.Terminals == nil {
		s.Terminals = map[string]rawrkit.TerminalEntry{}
	}
	for name, raw := range s.LegacyVMs {
		var old struct {
			VM      model.VM        `json:"vm"`
			Binding rawrkit.Binding `json:"binding"`
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return err
		}
		if old.VM.Name != name {
			return fmt.Errorf("invalid legacy VM association; live work preserved")
		}
		spec, err := Terminal(old.VM, s.Context.Root)
		if err != nil {
			return err
		}
		if resolved, err := exec.LookPath(spec.Argv[0]); err == nil {
			spec.Argv[0] = resolved
		}
		if existing, ok := s.Terminals[spec.ID]; ok && len(existing.Spec.Argv) > 0 {
			if resolved, err := exec.LookPath(existing.Spec.Argv[0]); err == nil {
				existing.Spec.Argv[0] = resolved
			}
			existing.Spec.Cwd = filepath.Clean(existing.Spec.Cwd)
			s.Terminals[spec.ID] = existing
		}
		spec.Cwd = filepath.Clean(spec.Cwd)
		if existing, ok := s.Terminals[spec.ID]; ok && (existing.Binding != old.Binding || !reflect.DeepEqual(existing.Spec, spec)) {
			return fmt.Errorf("conflicting legacy terminal associations or recipes; live work preserved")
		}
		s.Terminals[spec.ID] = rawrkit.TerminalEntry{Spec: spec, Binding: old.Binding}
	}
	if s.LegacySelected != "" {
		if s.Selected != "" && s.Selected != "exe:"+s.LegacySelected {
			return fmt.Errorf("conflicting selected terminal identities; live work preserved")
		}
		s.Selected = "exe:" + s.LegacySelected
	}
	s.LegacyVMs = nil
	s.LegacySelected = ""
	return nil
}
