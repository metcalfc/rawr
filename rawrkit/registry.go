package rawrkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (m *Manager) executable(id string) (string, error) {
	d, ok := m.definitions[id]
	if !ok {
		return "", fmt.Errorf("unregistered rawr %s", id)
	}
	exe := filepath.Join(m.BinaryDir, d.ID)
	if len(d.Command) > 0 {
		exe = d.Command[0]
	}
	if !filepath.IsAbs(exe) && strings.ContainsAny(exe, "/\\") {
		var err error
		exe, err = filepath.Abs(exe)
		if err != nil {
			return "", err
		}
	}
	resolved, err := exec.LookPath(exe)
	if err != nil {
		return "", fmt.Errorf("app executable unavailable: %s", exe)
	}
	return resolved, nil
}

func validateTerminal(s TerminalSpec) error {
	if s.ID == "" || len(s.ID) > 256 || strings.ContainsAny(s.ID, "\x00\r\n") || s.Title == "" || len(s.Argv) == 0 || !filepath.IsAbs(s.Cwd) {
		return errors.New("managed terminal needs an ID, title, literal argv, and absolute cwd")
	}
	for _, arg := range s.Argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("terminal argv contains NUL")
		}
	}
	st, err := os.Stat(s.Cwd)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("terminal cwd is inaccessible: %s", s.Cwd)
	}
	return nil
}

// normalizeState translates registered aliases in memory. Persistent changes
// happen only with a successful explicit operation. Unregistered app identities
// remain opaque so independently built rawrs can share a workspace safely.
func (m *Manager) normalizeState(s *State) error {
	canonical := func(id string) string {
		if d, ok := m.definitions[id]; ok {
			return d.ID
		}
		return id
	}
	apps := map[string]Binding{}
	for id, b := range s.Apps {
		key := canonical(id)
		if old, exists := apps[key]; exists && old != b {
			return fmt.Errorf("multiple retained panes claim rawr %s; preserved", key)
		}
		apps[key] = b
	}
	if s.Apps != nil {
		s.Apps = apps
	}
	for _, places := range []*map[string]Placement{&s.Hidden, &s.Placements} {
		if *places == nil {
			continue
		}
		out := map[string]Placement{}
		for id, p := range *places {
			key := canonical(id)
			if _, exists := out[key]; exists {
				return fmt.Errorf("ambiguous alias placement for %s", key)
			}
			for si := range p.Chain {
				for ri, ref := range p.Chain[si].Siblings {
					if strings.HasPrefix(ref, "@") && ref != "@main" {
						p.Chain[si].Siblings[ri] = "@" + canonical(strings.TrimPrefix(ref, "@"))
					}
				}
			}
			out[key] = p
		}
		*places = out
	}
	return nil
}
