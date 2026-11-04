// Package panes owns only native Rex pane associations. It never injects input,
// emulates a terminal, or manages arbitrary application compatibility.
package rawrkit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"encoding/json"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type Manager struct {
	Rex                 *rex.Client
	StateDir, BinaryDir string
	definitions         map[string]Definition
	initErr             error
	// MigrateState is an optional app-owned adapter for legacy provider metadata.
	MigrateState func(*State) error
}

func New(client *rex.Client, stateDir, binaryDir string, defs ...Definition) *Manager {
	m := &Manager{Rex: client, StateDir: stateDir, BinaryDir: binaryDir, definitions: map[string]Definition{}}
	for _, d := range defs {
		if err := m.Register(d); err != nil {
			m.initErr = err
			break
		}
	}
	return m
}

type Binding struct {
	BlockID string `json:"block_id"`
	Label   string `json:"label"`
	PID     int    `json:"pid"`
	Path    string `json:"path"`
}
type Pending struct {
	Operation string                    `json:"operation"`
	Label     string                    `json:"label,omitempty"`
	BlockID   string                    `json:"block_id,omitempty"`
	Blocks    []string                  `json:"blocks,omitempty"`
	Witnesses map[string]PendingWitness `json:"witnesses,omitempty"`
}

// PendingWitness records only native ownership and process identity before a
// multi-block show operation. It contains no terminal contents or command argv.
type PendingWitness struct {
	Label          string `json:"label"`
	HostID         string `json:"host_id"`
	ChildPID       int    `json:"child_pid"`
	ChildPath      string `json:"child_path"`
	ForegroundPID  int    `json:"foreground_pid,omitempty"`
	ForegroundPath string `json:"foreground_path,omitempty"`
	Exited         bool   `json:"exited,omitempty"`
}
type State struct {
	Schema         int                        `json:"schema"`
	Context        Context                    `json:"context"`
	Host           string                     `json:"host"`
	Generation     string                     `json:"generation"`
	MainBlock      string                     `json:"main_block"`
	LocalBlock     string                     `json:"local_block,omitempty"`
	Selected       string                     `json:"selected_terminal,omitempty"`
	LegacySelected string                     `json:"selected_vm,omitempty"`
	Apps           map[string]Binding         `json:"apps"`
	Terminals      map[string]TerminalEntry   `json:"terminals"`
	LegacyVMs      map[string]json.RawMessage `json:"vms,omitempty"`
	Hidden         map[string]Placement       `json:"hidden"`
	Placements     map[string]Placement       `json:"placements"`
	LastShape      string                     `json:"last_shape,omitempty"`
	Pending        *Pending                   `json:"pending,omitempty"`
}
type Snapshot struct {
	MainBlock        string             `json:"main_block"`
	SelectedTerminal *TerminalSpec      `json:"selected_terminal,omitempty"`
	Apps             map[string]string  `json:"apps"`
	Hidden           []string           `json:"hidden,omitempty"`
	Pending          *Pending           `json:"pending,omitempty"`
	Restarted        bool               `json:"restarted,omitempty"`
	Bindings         map[string]Binding `json:"-"`
}
type Result struct {
	Result  string `json:"result"`
	BlockID string `json:"block_id,omitempty"`
	Visible bool   `json:"visible"`
	Reused  bool   `json:"reused"`
}

func token() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func statePath(dir string, i rex.Identity, c Context) string {
	key := sha256.Sum256([]byte(i.HostID + "\x00" + c.Session + "\x00" + c.Scope))
	return filepath.Join(dir, hex.EncodeToString(key[:16])+".json")
}

// ScopeForRoot is shared by plain commands and the optional project layer.
// Sessions distinguish separate workspaces using the same checkout.
func ScopeForRoot(root string) string {
	digest := sha256.Sum256([]byte(root))
	return "root-" + hex.EncodeToString(digest[:12])
}
func validateContext(c Context) error {
	if c.Session == "" {
		return errors.New("run inside Rex, pass --session and --anchor, or use --tui for the standalone terminal UI")
	}
	if c.Scope == "" || c.Root == "" || !filepath.IsAbs(c.Root) {
		return errors.New("an absolute project root and stable scope are required")
	}
	st, e := os.Stat(c.Root)
	if e != nil || !st.IsDir() {
		return fmt.Errorf("project root is inaccessible: %s", c.Root)
	}
	return nil
}
func (m *Manager) withState(ctx context.Context, c Context, mutate bool, fn func(*State, rex.Identity, rex.View, string) error) error {
	if err := validateContext(c); err != nil {
		return err
	}
	i, err := m.Rex.Identity(ctx)
	if err != nil {
		return err
	}
	path := statePath(m.StateDir, i, c)
	return storage.WithLock(path+".lock", func() error {
		s := State{}
		err := storage.ReadJSON(path, &s)
		if errors.Is(err, os.ErrNotExist) {
			s = State{Schema: 1, Context: c, Host: i.HostID, Generation: i.InstanceID, MainBlock: c.Anchor, LocalBlock: c.Anchor, Apps: map[string]Binding{}, Terminals: map[string]TerminalEntry{}, Hidden: map[string]Placement{}, Placements: map[string]Placement{}}
		} else if err != nil {
			return err
		}
		if s.Schema != 1 || s.Host != i.HostID || s.Context.Session != c.Session || s.Context.Scope != c.Scope || s.Context.Root != c.Root {
			return errors.New("pane state identity/context mismatch; existing work was preserved")
		}
		if mutate && s.Generation != i.InstanceID {
			return errors.New("Rex restarted: retained terminal IDs may now contain fresh shells. Existing work was preserved; use a new scope or inspect the old state before explicitly recreating tools")
		}
		if m.initErr != nil {
			return m.initErr
		}
		if err := m.normalizeState(&s); err != nil {
			return err
		}
		if m.MigrateState != nil {
			if err := m.MigrateState(&s); err != nil {
				return err
			}
		}
		if len(s.LegacyVMs) > 0 || s.LegacySelected != "" {
			return errors.New("legacy provider metadata requires an app migration adapter; retained work preserved")
		}
		if s.Terminals == nil {
			s.Terminals = map[string]TerminalEntry{}
		}
		if s.Apps == nil || s.Hidden == nil || s.Placements == nil {
			return errors.New("invalid pane state maps")
		}
		if err := s.validate(); err != nil {
			return err
		}
		if c.Repository != "" {
			if s.Context.Repository != "" && s.Context.Repository != c.Repository {
				return errors.New("repository differs from this anchored workspace; use a separate --scope for another repository")
			}
			if s.Context.Repository == "" {
				s.Context.Repository = c.Repository
			}
		}
		v, err := m.Rex.View(ctx, c.Session)
		if err != nil {
			return err
		}
		if mutate && s.Pending != nil {
			recovered, err := m.reconcileExitedApp(ctx, &s, i, v, path)
			if err != nil {
				return err
			}
			if !recovered {
				return fmt.Errorf("interrupted %s operation; inspect pending IDs in %s before retrying; live work was preserved", s.Pending.Operation, path)
			}
		}
		return fn(&s, i, v, path)
	})
}

func pendingWitness(block rex.Block, process rex.Process) PendingWitness {
	w := PendingWitness{Label: block.Label, HostID: block.HostID, Exited: len(process.LastExit) > 0 && string(process.LastExit) != "null"}
	if process.Child != nil {
		w.ChildPID, w.ChildPath = process.Child.PID, process.Child.Path
	}
	if process.Foreground != nil {
		w.ForegroundPID, w.ForegroundPath = process.Foreground.PID, process.Foreground.Path
	}
	return w
}

func (w PendingWitness) verified() bool {
	if w.Label == "" || w.HostID == "" {
		return false
	}
	if w.Exited {
		return w.ChildPID == 0 && w.ChildPath == "" && w.ForegroundPID == 0 && w.ForegroundPath == ""
	}
	return w.ChildPID > 0 && filepath.IsAbs(w.ChildPath) &&
		((w.ForegroundPID == 0 && w.ForegroundPath == "") || (w.ForegroundPID > 0 && filepath.IsAbs(w.ForegroundPath)))
}

// reconcileExitedApp clears only a known app journal whose returned/bound
// surface no longer exists. It performs no native mutation or automatic retry.
// Unknown creation IDs, other terminal roles and partially changed show work
// retain their journal for explicit inspection.
func (m *Manager) reconcileExitedApp(ctx context.Context, s *State, i rex.Identity, v rex.View, path string) (bool, error) {
	p := s.Pending
	if p.BlockID == "" || p.BlockID == s.MainBlock || p.BlockID == s.LocalBlock {
		return false, nil
	}
	for _, terminal := range s.Terminals {
		if terminal.Binding.BlockID == p.BlockID {
			return false, nil
		}
	}
	kind := ""
	if strings.HasPrefix(p.Operation, "open ") {
		kind = strings.TrimPrefix(p.Operation, "open ")
		if p.Label == "" || !strings.HasPrefix(p.Label, "rexapp:"+kind+":") {
			return false, nil
		}
		if b, exists := s.Apps[kind]; exists && b.BlockID != p.BlockID {
			return false, nil
		}
	} else if p.Operation == "hide" || p.Operation == "show" {
		for name, binding := range s.Apps {
			if binding.BlockID == p.BlockID {
				kind = name
			}
		}
	} else {
		return false, nil
	}
	if definition, registered := m.definitions[kind]; !registered || definition.ID != kind {
		return false, nil
	}
	for name, binding := range s.Apps {
		if binding.BlockID == p.BlockID && name != kind {
			return false, nil
		}
	}
	visible := v.Blocks()
	if _, exists := visible[p.BlockID]; exists {
		return false, nil
	}
	inventory, err := m.inventory(ctx, s.Context.Session)
	if err != nil {
		return false, err
	}
	if _, exists := inventory[p.BlockID]; exists {
		return false, nil
	}
	if p.Operation == "show" {
		if len(p.Blocks) == 0 || len(p.Witnesses) != len(p.Blocks) {
			return false, nil
		}
		seen := map[string]bool{}
		for _, bid := range p.Blocks {
			if seen[bid] {
				return false, nil
			}
			seen[bid] = true
			witness, exists := p.Witnesses[bid]
			if !exists || !witness.verified() || witness.HostID != i.HostID {
				return false, nil
			}
			block, survives := inventory[bid]
			if !survives {
				if _, visible := visible[bid]; visible {
					return false, nil
				}
				continue
			}
			if _, shown := visible[bid]; !shown || block.Placement != "placed" {
				return false, nil
			}
			process, err := m.Rex.Process(ctx, s.Context.Session, bid)
			if err != nil {
				return false, err
			}
			if pendingWitness(block, process) != witness {
				return false, nil
			}
		}
		if !seen[p.BlockID] {
			return false, nil
		}
	}
	s.forgetApp(kind)
	s.Pending = nil
	s.LastShape = shape(s, v)
	return true, storage.WriteJSON(path, s)
}

func (s *State) forgetApp(kind string) {
	delete(s.Apps, kind)
	delete(s.Hidden, kind)
	delete(s.Placements, kind)
	if len(s.Hidden) == 0 {
		s.Placements = map[string]Placement{}
	}
}

func (s *State) validate() error {
	seen := map[string]bool{}
	binding := func(b Binding) error {
		if b.BlockID == "" || b.Label == "" || b.PID <= 0 || !filepath.IsAbs(b.Path) || seen[b.BlockID] {
			return errors.New("invalid or duplicate native process binding; work preserved")
		}
		seen[b.BlockID] = true
		return nil
	}
	for kind, b := range s.Apps {
		if !appID.MatchString(kind) {
			return errors.New("invalid saved app kind")
		}
		if err := binding(b); err != nil {
			return err
		}
		if b.BlockID == s.MainBlock || b.BlockID == s.LocalBlock {
			return errors.New("app cannot occupy the saved work association")
		}
	}
	for name, e := range s.Terminals {
		if name == "" || e.Spec.ID != name {
			return errors.New("invalid saved terminal association")
		}
		if err := binding(e.Binding); err != nil {
			return err
		}
	}
	if s.Selected != "" {
		if _, ok := s.Terminals[s.Selected]; !ok {
			return errors.New("selected terminal has no declared association")
		}
	}
	for _, places := range []map[string]Placement{s.Hidden, s.Placements} {
		for kind, p := range places {
			if _, ok := s.Apps[kind]; !ok || !appID.MatchString(kind) || p.WindowID == "" || len(p.Chain) == 0 || len(p.Chain) > 40 {
				return errors.New("invalid retained pane placement; work preserved")
			}
			for _, step := range p.Chain {
				if (step.Direction != "horizontal" && step.Direction != "vertical") || (step.Side != "before" && step.Side != "after") || step.Ratio <= 0 || step.Ratio >= 1 || math.IsNaN(step.Ratio) || math.IsInf(step.Ratio, 0) || len(step.Siblings) == 0 {
					return errors.New("invalid retained split geometry; work preserved")
				}
				for _, ref := range step.Siblings {
					if ref == "" {
						return errors.New("empty retained pane reference")
					}
				}
			}
		}
	}
	return nil
}
func (m *Manager) Snapshot(ctx context.Context, c Context) (Snapshot, error) {
	var out Snapshot
	err := m.withState(ctx, c, false, func(s *State, i rex.Identity, v rex.View, _ string) error {
		out = Snapshot{MainBlock: s.MainBlock, Apps: map[string]string{}, Bindings: map[string]Binding{}, Pending: s.Pending, Restarted: s.Generation != i.InstanceID}
		for k, b := range s.Apps {
			out.Apps[k] = b.BlockID
			out.Bindings[k] = b
		}
		if len(s.Hidden) > 0 {
			inv, err := m.inventory(ctx, c.Session)
			if err != nil {
				return err
			}
			for k := range s.Hidden {
				if b, ok := inv[s.Apps[k].BlockID]; ok && b.Placement == "detached" {
					if _, visible := v.Blocks()[b.ID]; !visible {
						out.Hidden = append(out.Hidden, k)
					}
				}
			}
		}
		if e, ok := s.Terminals[s.Selected]; ok {
			spec := e.Spec
			out.SelectedTerminal = &spec
		}
		return nil
	})
	return out, err
}
func (m *Manager) Status(ctx context.Context, c Context) (Snapshot, error) {
	return m.Snapshot(ctx, c)
}
func samePath(a, b string) bool {
	aa, e := filepath.EvalSymlinks(a)
	if e != nil {
		aa = filepath.Clean(a)
	}
	bb, e := filepath.EvalSymlinks(b)
	if e != nil {
		bb = filepath.Clean(b)
	}
	return aa == bb
}
func (m *Manager) capture(ctx context.Context, sid string, b rex.Block, expected string) (Binding, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		p, err := m.Rex.Process(ctx, sid, b.ID)
		if err != nil {
			return Binding{}, err
		}
		if p.Child != nil && p.Child.PID > 0 && samePath(p.Child.Path, expected) {
			return Binding{b.ID, b.Label, p.Child.PID, p.Child.Path}, nil
		}
		if time.Now().After(deadline) {
			return Binding{}, fmt.Errorf("pane %s has no verified %s process; its surface was preserved", b.ID, filepath.Base(expected))
		}
		select {
		case <-ctx.Done():
			return Binding{}, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
}
func (m *Manager) healthy(ctx context.Context, s *State, i rex.Identity, b Binding, inventory map[string]rex.Block) error {
	entry, ok := inventory[b.BlockID]
	if !ok {
		return os.ErrNotExist
	}
	if entry.Label != b.Label || entry.HostID != i.HostID {
		return fmt.Errorf("pane ownership changed for %s; preserved", b.BlockID)
	}
	p, err := m.Rex.Process(ctx, s.Context.Session, b.BlockID)
	if err != nil {
		return err
	}
	if p.Child == nil || p.Child.PID != b.PID || !samePath(p.Child.Path, b.Path) || (len(p.LastExit) > 0 && string(p.LastExit) != "null") {
		return fmt.Errorf("pane %s contains changed or restarted work; preserved", b.BlockID)
	}
	return nil
}
func (m *Manager) inventory(ctx context.Context, sid string) (map[string]rex.Block, error) {
	list, err := m.Rex.Inventory(ctx, sid)
	if err != nil {
		return nil, err
	}
	out := map[string]rex.Block{}
	for _, b := range list {
		out[b.ID] = b
	}
	return out, nil
}

// Adopt binds IDs returned by a project-owned creation, never by title matching.
func (m *Manager) Adopt(ctx context.Context, c Context, kind, bid, anchor string) error {
	var err error
	kind, err = m.Canonical(kind)
	if err != nil {
		return err
	}
	return m.withState(ctx, c, true, func(s *State, i rex.Identity, v rex.View, path string) error {
		if _, ok := v.Blocks()[anchor]; !ok {
			return errors.New("project anchor is not visible")
		}
		if old, ok := s.Apps[kind]; ok && old.BlockID != bid {
			all, err := m.inventory(ctx, c.Session)
			if err != nil {
				return err
			}
			if _, exists := all[old.BlockID]; exists {
				return errors.New("scope already has a different live or detached app pane; preserved")
			}
			delete(s.Hidden, kind)
		}
		b, ok := v.Blocks()[bid]
		if !ok || b.HostID != i.HostID {
			return errors.New("returned app block is unavailable on this host")
		}
		exe, err := m.executable(kind)
		if err != nil {
			return err
		}
		binding, err := m.capture(ctx, c.Session, b, exe)
		if err != nil {
			return err
		}
		s.Apps[kind] = binding
		if s.MainBlock == "" {
			s.MainBlock = anchor
			s.LocalBlock = anchor
		}
		return storage.WriteJSON(path, s)
	})
}
