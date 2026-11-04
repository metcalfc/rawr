package rawrkit

import (
	"context"
	"errors"
	"os/exec"
	"reflect"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

// ActivateTerminal opens/reuses a declared terminal in the anchored main slot.
func (m *Manager) ActivateTerminal(ctx context.Context, c Context, spec TerminalSpec) error {
	if err := validateTerminal(spec); err != nil {
		return err
	}
	executable, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return err
	}
	argv := append([]string(nil), spec.Argv...)
	argv[0] = executable
	spec.Argv = argv
	return m.withState(ctx, c, true, func(s *State, i rex.Identity, v rex.View, path string) error {
		live := v.Blocks()
		old, ok := live[s.MainBlock]
		if !ok {
			return errors.New("anchored main work pane is unavailable; live work preserved")
		}
		inv, err := m.inventory(ctx, c.Session)
		if err != nil {
			return err
		}
		entry, exists := s.Terminals[spec.ID]
		target := entry.Binding.BlockID
		if exists {
			if _, present := inv[target]; present {
				if !sameTerminal(entry.Spec, spec) {
					return errors.New("managed terminal ID already has a different live command or context; use a new ID; existing work preserved")
				}
				if err = m.healthy(ctx, s, i, entry.Binding, inv); err != nil {
					// A deliberately quit viewer leaves an exited native surface.
					// Create a fresh view while retaining that surface. A shell or
					// any other live/unknown replacement must still fail closed.
					b := inv[target]
					p, probeErr := m.Rex.Process(ctx, c.Session, target)
					if probeErr != nil || b.HostID != i.HostID || b.Label != entry.Binding.Label || p.Child != nil || p.Foreground != nil || len(p.LastExit) == 0 || string(p.LastExit) == "null" {
						return err
					}
					exists = false
				}
				if exists && target == s.MainBlock {
					return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": target}, nil)
				}
				if _, placed := live[target]; exists && !placed {
					return errors.New("managed terminal was detached outside these controls; preserved")
				}
			} else {
				exists = false
			}
		}
		previous := s.Selected
		s.Pending = &Pending{Operation: "activate terminal", BlockID: s.MainBlock, Label: "rawrkit:terminal:" + token()}
		if err = storage.WriteJSON(path, s); err != nil {
			return err
		}
		if !exists {
			var created rex.Created
			if err = m.Rex.Call(ctx, "session.new_window", map[string]any{"session_id": c.Session, "window_label": spec.Title, "focus": false, "layout": rex.Leaf(s.Pending.Label, argv, spec.Cwd)}, &created); err != nil {
				return err
			}
			if len(created.BlockIDs) != 1 {
				return errors.New("ambiguous terminal creation; inspect pending label before retrying")
			}
			target = created.BlockIDs[0]
			s.Pending.Blocks = []string{s.MainBlock, target}
			if err = storage.WriteJSON(path, s); err != nil {
				return err
			}
			inv, err = m.inventory(ctx, c.Session)
			if err != nil {
				return err
			}
			b, ok := inv[target]
			if !ok || b.HostID != i.HostID || b.Label != s.Pending.Label {
				return errors.New("terminal creation ownership mismatch; preserved")
			}
			binding, err := m.capture(ctx, c.Session, b, executable)
			if err != nil {
				return err
			}
			entry = TerminalEntry{spec, binding}
			s.Terminals[spec.ID] = entry
		}
		before, err := m.Rex.View(ctx, c.Session)
		if err != nil {
			return err
		}
		archive := before.Blocks()[target].WindowID
		if archive == "" || archive == old.WindowID {
			return errors.New("terminal and work must occupy distinct native tabs before swapping")
		}
		if err = m.Rex.Call(ctx, "session.swap_blocks", map[string]any{"session_id": c.Session, "block_id": s.MainBlock, "other_block_id": target}, nil); err != nil {
			return err
		}
		title := "Work · previous"
		if previous != "" {
			title = s.Terminals[previous].Spec.Title
		}
		if err = m.Rex.Call(ctx, "session.set_window_label", map[string]any{"session_id": c.Session, "window_id": archive, "label": title}, nil); err != nil {
			return err
		}
		after, err := m.Rex.View(ctx, c.Session)
		if err != nil {
			return err
		}
		if after.Blocks()[target].WindowID != old.WindowID {
			return errors.New("terminal did not reach anchored main window; inspect journal")
		}
		// The swap changes IDs, not the geometry. Never issue resize calls here.
		s.MainBlock = target
		s.Selected = spec.ID
		s.Pending = nil
		s.LastShape = shape(s, after)
		if err = storage.WriteJSON(path, s); err != nil {
			return err
		}
		return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": target}, nil)
	})
}

func sameTerminal(a, b TerminalSpec) bool {
	if a.ID != b.ID || a.Title != b.Title || !samePath(a.Cwd, b.Cwd) || len(a.Argv) != len(b.Argv) || len(a.Argv) == 0 || !reflect.DeepEqual(a.Metadata, b.Metadata) {
		return false
	}
	resolve := func(s string) string {
		if p, err := exec.LookPath(s); err == nil {
			return p
		}
		return s
	}
	if !samePath(resolve(a.Argv[0]), resolve(b.Argv[0])) {
		return false
	}
	return reflect.DeepEqual(a.Argv[1:], b.Argv[1:])
}

// LocalTerm brings the original local work back to the center, retaining any
// programs running there. If it was closed, this explicit request creates one.
func (m *Manager) LocalTerm(ctx context.Context, c Context) (Result, error) {
	var out Result
	err := m.withState(ctx, c, true, func(s *State, _ rex.Identity, v rex.View, path string) error {
		live := v.Blocks()
		old, ok := live[s.MainBlock]
		if !ok {
			return errors.New("anchored main work pane is unavailable")
		}
		target := s.LocalBlock
		if target == s.MainBlock {
			out = Result{"complete", target, true, true}
			return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": target}, nil)
		}
		inv, err := m.inventory(ctx, c.Session)
		if err != nil {
			return err
		}
		if b, exists := inv[target]; exists && b.Placement == "detached" {
			return errors.New("local work was detached outside these controls; preserved")
		}
		s.Pending = &Pending{Operation: "local terminal", BlockID: s.MainBlock, Label: "rexapp:local:" + token()}
		if err = storage.WriteJSON(path, s); err != nil {
			return err
		}
		reused := true
		if _, ok := live[target]; !ok {
			var created rex.Created
			if err = m.Rex.Call(ctx, "session.new_window", map[string]any{"session_id": c.Session, "window_label": "Local work", "focus": false, "layout": rex.Leaf(s.Pending.Label, nil, c.Root)}, &created); err != nil {
				return err
			}
			if len(created.BlockIDs) != 1 {
				return errors.New("ambiguous local terminal creation; inspect journal")
			}
			target = created.BlockIDs[0]
			reused = false
			s.Pending.Blocks = []string{s.MainBlock, target}
			if err = storage.WriteJSON(path, s); err != nil {
				return err
			}
		}
		before, err := m.Rex.View(ctx, c.Session)
		if err != nil {
			return err
		}
		archive := before.Blocks()[target].WindowID
		if archive == old.WindowID {
			return errors.New("local work was moved alongside main; preserved")
		}
		if err = m.Rex.Call(ctx, "session.swap_blocks", map[string]any{"session_id": c.Session, "block_id": s.MainBlock, "other_block_id": target}, nil); err != nil {
			return err
		}
		title := "Work · previous"
		if s.Selected != "" {
			title = s.Terminals[s.Selected].Spec.Title
		}
		if err = m.Rex.Call(ctx, "session.set_window_label", map[string]any{"session_id": c.Session, "window_id": archive, "label": title}, nil); err != nil {
			return err
		}
		s.MainBlock = target
		s.LocalBlock = target
		s.Selected = ""
		s.Pending = nil
		after, err := m.Rex.View(ctx, c.Session)
		if err != nil {
			return err
		}
		s.LastShape = shape(s, after)
		if err = storage.WriteJSON(path, s); err != nil {
			return err
		}
		out = Result{"complete", target, true, reused}
		return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": target}, nil)
	})
	return out, err
}
