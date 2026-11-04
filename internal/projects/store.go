package projects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"context"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

// Manager stores deliberately saved definitions separately from observed Rex IDs.
// A Manager never discovers ownership from a user-editable terminal title.
type Manager struct {
	Rex       *rex.Client
	ConfigDir string
	StateDir  string
	BinaryDir string
	AppsDir   string
}

func New(client *rex.Client, configDir, stateDir, binaryDir string) *Manager {
	return &Manager{Rex: client, ConfigDir: configDir, StateDir: stateDir, BinaryDir: binaryDir, AppsDir: filepath.Join(stateDir, "apps")}
}

type binding struct {
	BlockID    string           `json:"block_id"`
	Label      string           `json:"label"`
	InstanceID string           `json:"instance_id"`
	RecipeKind string           `json:"recipe_kind,omitempty"`
	PID        int              `json:"pid,omitempty"`
	Program    *rex.ProcessInfo `json:"program,omitempty"`
}
type pending struct {
	Kind  string `json:"kind"`
	Role  string `json:"role,omitempty"`
	Label string `json:"label,omitempty"`
	Token string `json:"token,omitempty"`
}
type observed struct {
	Schema        int                `json:"schema"`
	HostID        string             `json:"host_id,omitempty"`
	InstanceID    string             `json:"instance_id,omitempty"`
	SessionID     string             `json:"session_id,omitempty"`
	SessionLabel  string             `json:"session_label,omitempty"`
	LastWork      string             `json:"last_work,omitempty"`
	Roles         map[string]binding `json:"roles"`
	Pending       *pending           `json:"pending,omitempty"`
	LayoutPending bool               `json:"layout_pending,omitempty"`
}

func (m *Manager) projectPath(id string) string { return filepath.Join(m.ConfigDir, id+".json") }
func (m *Manager) statePath(id string) string   { return filepath.Join(m.StateDir, id+".json") }
func (m *Manager) lock(id string, fn func() error) error {
	return storage.WithLock(filepath.Join(m.ConfigDir, "."+id+".lock"), func() error {
		// Do not permit a different runtime directory to silently lose ownership.
		// A copied definition may use an empty config directory for a new workspace.
		if err := storage.WithLock(filepath.Join(m.ConfigDir, ".runtime-location.lock"), func() error { return m.checkLocation(true) }); err != nil {
			return err
		}
		return fn()
	})
}

func (m *Manager) checkLocation(create bool) error {
	var location struct {
		StateDir string `json:"state_dir"`
	}
	path := filepath.Join(m.ConfigDir, ".runtime-location.json")
	wanted, err := filepath.Abs(m.StateDir)
	if err != nil {
		return err
	}
	if err = storage.ReadJSON(path, &location); errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil
		}
		location.StateDir = wanted
		return storage.WriteJSON(path, location)
	} else if err != nil {
		return err
	}
	if location.StateDir != wanted {
		return fmt.Errorf("this config uses runtime state at %s; use that --state-dir or explicitly migrate the mapping", location.StateDir)
	}
	return nil
}

func (m *Manager) Create(name, root, repository, vm string, kinds []string) (Project, error) {
	p, err := newProject(name, root, repository, vm, kinds)
	if err != nil {
		return p, err
	}
	err = m.lock("projects", func() error {
		all, err := m.List()
		if err != nil {
			return err
		}
		for _, other := range all {
			if strings.EqualFold(other.Name, p.Name) {
				return fmt.Errorf("project name already exists: %s", p.Name)
			}
		}
		return storage.WriteJSON(m.projectPath(p.ID), p)
	})
	return p, err
}

func (m *Manager) List() ([]Project, error) {
	entries, err := os.ReadDir(m.ConfigDir)
	if errors.Is(err, os.ErrNotExist) {
		return []Project{}, nil
	}
	if err != nil {
		return nil, err
	}
	projects := []Project{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var p Project
		if err = storage.ReadJSON(filepath.Join(m.ConfigDir, entry.Name()), &p); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		normalizeProject(&p)
		if err = Validate(p); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if entry.Name() != p.ID+".json" {
			return nil, fmt.Errorf("project filename and ID differ: %s", entry.Name())
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}

func (m *Manager) Get(value string) (Project, error) {
	all, err := m.List()
	if err != nil {
		return Project{}, err
	}
	var found *Project
	for _, p := range all {
		if p.ID == value || p.Name == value {
			if found != nil {
				return Project{}, fmt.Errorf("ambiguous project %q; use its full ID", value)
			}
			v := p
			found = &v
		}
	}
	if found == nil {
		return Project{}, fmt.Errorf("unknown project %q", value)
	}
	return *found, nil
}

// Link deliberately changes project metadata. Selecting a VM shell remains a
// separate native pane operation; linking never writes to or replaces a terminal.
func (m *Manager) Link(_ context.Context, id string, vm model.VM) (Project, error) {
	p, err := m.Get(id)
	if err != nil {
		return Project{}, err
	}
	if strings.TrimSpace(vm.Name) == "" || strings.ContainsAny(vm.Name, "\x00\r\n") {
		return Project{}, fmt.Errorf("invalid linked VM name")
	}
	err = m.lock(p.ID, func() error {
		p, err = m.Get(p.ID)
		if err != nil {
			return err
		}
		before := p.Revision
		p.VM = vm.Name
		p.Revision++
		return m.write(p, before)
	})
	return p, err
}

func (m *Manager) readObserved(p Project) (observed, error) {
	o := observed{Schema: Schema, Roles: map[string]binding{}}
	err := storage.ReadJSON(m.statePath(p.ID), &o)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	if o.Schema != Schema || o.Roles == nil {
		return o, fmt.Errorf("invalid/legacy project runtime mapping; do not adopt existing Python sessions automatically")
	}
	if o.SessionID != "" && (o.SessionLabel == "" || o.HostID == "") {
		return o, fmt.Errorf("incomplete runtime mapping; inspect it before reopening")
	}
	for role, b := range o.Roles {
		if role == "" || b.BlockID == "" || b.InstanceID == "" {
			return o, fmt.Errorf("invalid role binding; preserve the runtime file for diagnosis")
		}
	}
	if o.Pending != nil && o.Pending.Kind != "session" && o.Pending.Kind != "split" {
		return o, fmt.Errorf("invalid pending operation record")
	}
	return o, nil
}
func (m *Manager) record(p Project, o observed) error { return storage.WriteJSON(m.statePath(p.ID), o) }
func (m *Manager) write(p Project, expected int) error {
	current, err := m.Get(p.ID)
	if err != nil {
		return err
	}
	if current.Revision != expected {
		return fmt.Errorf("stale save revision: expected %d, found %d", expected, current.Revision)
	}
	if err = Validate(p); err != nil {
		return err
	}
	// Keep a readable previous definition; neither file contains process output.
	if err = storage.WriteJSON(m.projectPath(p.ID)+".previous", current); err != nil {
		return err
	}
	return storage.WriteJSON(m.projectPath(p.ID), p)
}
