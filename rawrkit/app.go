// Package rawrkit provides native Rex pane lifecycle for independently compiled
// terminal apps. It has no dependency on Rawr projects or provider integrations.
package rawrkit

import (
	"fmt"
	"regexp"
	"strings"
)

// Context is anchored at launch and contains identifiers, never credentials or
// terminal output. Repository and VM are optional, opaque app context values.
type Context struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	Repository string `json:"repository,omitempty"`
	VM         string `json:"vm,omitempty"`
	Scope      string `json:"scope"`
	Session    string `json:"session_id,omitempty"`
	Anchor     string `json:"anchor_block_id,omitempty"`
	Rex        string `json:"rex,omitempty"`
	Server     string `json:"server,omitempty"`
	Client     string `json:"client,omitempty"`
	StateDir   string `json:"state_dir,omitempty"`
}

// Definition registers an app without teaching the pane manager its name.
// Command defaults to BinaryDir/ID. Native launches append --tui --context-file
// PATH; the app reads that Context and renders on its own terminal.
type Definition struct {
	ID        string
	Title     string
	Aliases   []string
	Command   []string
	Placement PlacementSpec
}

// PlacementSpec is a default for first opening, not a continuously enforced
// layout. Ratio is the fraction before the native split. An unavailable
// AnchorApp falls back to a horizontal split after the main work pane at .7.
type PlacementSpec struct {
	Direction  string
	Side       string
	Ratio      float64
	AnchorApp  string
	WholeLayer bool
}

var appID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func (m *Manager) Register(d Definition) error {
	if !appID.MatchString(d.ID) {
		return fmt.Errorf("invalid rawr ID %q", d.ID)
	}
	if d.Placement.Direction == "" {
		d.Placement.Direction = "horizontal"
	}
	if d.Placement.Side == "" {
		d.Placement.Side = "after"
	}
	if d.Placement.Ratio == 0 {
		d.Placement.Ratio = .7
	}
	if (d.Placement.Direction != "horizontal" && d.Placement.Direction != "vertical") || (d.Placement.Side != "before" && d.Placement.Side != "after") || !(d.Placement.Ratio > 0 && d.Placement.Ratio < 1) {
		return fmt.Errorf("invalid placement for %s", d.ID)
	}
	if d.Title == "" {
		d.Title = d.ID
	}
	names := append([]string{d.ID}, d.Aliases...)
	seen := map[string]bool{}
	for _, name := range names {
		if !appID.MatchString(name) || seen[name] {
			return fmt.Errorf("invalid or duplicate rawr alias %q", name)
		}
		seen[name] = true
		if _, exists := m.definitions[name]; exists {
			return fmt.Errorf("rawr name already registered: %s", name)
		}
	}
	for _, arg := range d.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command contains NUL")
		}
	}
	if m.definitions == nil {
		m.definitions = map[string]Definition{}
	}
	d.Aliases = append([]string(nil), d.Aliases...)
	d.Command = append([]string(nil), d.Command...)
	for _, name := range names {
		m.definitions[name] = d
	}
	return nil
}

func (m *Manager) Canonical(id string) (string, error) {
	d, ok := m.definitions[id]
	if !ok {
		return "", fmt.Errorf("unregistered rawr %q", id)
	}
	return d.ID, nil
}
func (m *Manager) Definition(id string) (Definition, bool) {
	d, ok := m.definitions[id]
	d.Aliases = append([]string(nil), d.Aliases...)
	d.Command = append([]string(nil), d.Command...)
	return d, ok
}

// TerminalSpec requests a managed terminal in the center. An app supplies the
// literal command; Rex hosts it, while prior work survives in native history tabs.
type TerminalSpec struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Argv     []string          `json:"argv"`
	Cwd      string            `json:"cwd"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
type TerminalEntry struct {
	Spec    TerminalSpec `json:"spec"`
	Binding Binding      `json:"binding"`
}
