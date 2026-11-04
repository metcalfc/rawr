package rawrkit

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func (m *Manager) Open(ctx context.Context, c Context, kind string) (Result, error) {
	return m.action(ctx, c, kind, "open")
}
func (m *Manager) Toggle(ctx context.Context, c Context, kind string) (Result, error) {
	return m.action(ctx, c, kind, "toggle")
}
func (m *Manager) Hide(ctx context.Context, c Context, kind string) (Result, error) {
	return m.action(ctx, c, kind, "hide")
}
func (m *Manager) action(ctx context.Context, c Context, kind, action string) (Result, error) {
	var err error
	kind, err = m.Canonical(kind)
	if err != nil {
		return Result{}, err
	}
	var out Result
	err = m.withState(ctx, c, true, func(s *State, i rex.Identity, v rex.View, path string) error {
		inv, err := m.inventory(ctx, c.Session)
		if err != nil {
			return err
		}
		if b, ok := s.Apps[kind]; ok {
			if _, exists := inv[b.BlockID]; exists {
				if err = m.healthy(ctx, s, i, b, inv); err != nil {
					return err
				}
				if _, visible := v.Blocks()[b.BlockID]; visible {
					if action == "open" {
						if _, stale := s.Hidden[kind]; stale {
							delete(s.Hidden, kind)
							if len(s.Hidden) == 0 {
								s.Placements = map[string]Placement{}
							}
							if err = storage.WriteJSON(path, s); err != nil {
								return err
							}
						}
						out = Result{"complete", b.BlockID, true, true}
						return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": b.BlockID}, nil)
					}
					if err = remember(s, v); err != nil {
						return err
					}
					p := s.Placements[kind]
					s.Hidden[kind] = p
					s.Pending = &Pending{Operation: "hide", BlockID: b.BlockID}
					if err = storage.WriteJSON(path, s); err != nil {
						return err
					}
					if err = m.Rex.Call(ctx, "session.detach_block", map[string]any{"session_id": c.Session, "block_id": b.BlockID}, nil); err != nil {
						return err
					}
					after, err := m.Rex.View(ctx, c.Session)
					if err != nil {
						return err
					}
					if _, ok := after.Blocks()[b.BlockID]; ok {
						return errors.New("Rex did not hide the requested tool; preserved")
					}
					all, err := m.inventory(ctx, c.Session)
					if err != nil {
						return err
					}
					if all[b.BlockID].Placement != "detached" {
						return errors.New("hidden pane was not retained; inspect operation journal")
					}
					s.Pending = nil
					s.LastShape = shape(s, after)
					if err = storage.WriteJSON(path, s); err != nil {
						return err
					}
					out = Result{"complete", b.BlockID, false, true}
					if _, ok := after.Blocks()[s.MainBlock]; ok {
						return m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": s.MainBlock}, nil)
					}
					return nil
				}
				if action == "hide" {
					out = Result{"complete", b.BlockID, false, true}
					return nil
				}
				out, err = m.show(ctx, s, i, v, path, kind)
				return err
			}
			// Missing inventory means an explicitly closed surface. A detached one
			// was handled above, and must never be mistaken for missing work.
			s.forgetApp(kind)
		}
		if action == "hide" {
			out = Result{"complete", "", false, false}
			return nil
		}
		out, err = m.launch(ctx, s, i, v, path, kind)
		return err
	})
	return out, err
}
func (m *Manager) launch(ctx context.Context, s *State, i rex.Identity, v rex.View, path, kind string) (Result, error) {
	main := s.MainBlock
	if _, ok := v.Blocks()[main]; !ok {
		return Result{}, errors.New("the anchored main work pane is unavailable; choose --scope and --anchor explicitly for a new workspace")
	}
	exe, err := m.executable(kind)
	if err != nil {
		return Result{}, err
	}
	def := m.definitions[kind]
	argv := append([]string(nil), def.Command...)
	if len(argv) == 0 {
		argv = []string{exe}
	} else {
		argv[0] = exe
	}
	launchContext := s.Context
	launchContext.Anchor = main
	launchContext.Rex = m.Rex.Executable
	launchContext.Server = m.Rex.Server
	launchContext.Client = m.Rex.ClientID
	launchContext.StateDir = m.StateDir
	ctxPath := filepath.Join(m.StateDir, "contexts", token()+".json")
	if err = storage.WriteJSON(ctxPath, launchContext); err != nil {
		return Result{}, err
	}
	label := "rexapp:" + kind + ":" + token()
	s.Pending = &Pending{Operation: "open " + kind, Label: label}
	if err = storage.WriteJSON(path, s); err != nil {
		return Result{}, err
	}
	p := def.Placement
	anchor, direction, side, ratio := main, p.Direction, p.Side, p.Ratio
	if p.WholeLayer {
		for _, w := range v.Windows {
			for _, l := range w.Layers {
				if l.Kind == "tiled" && contains(l.Layout, main) && l.Layout.Split != nil {
					anchor = l.Layout.Split.ID
				}
			}
		}
	}
	if p.AnchorApp != "" {
		found := false
		anchorKind := p.AnchorApp
		if d, ok := m.definitions[anchorKind]; ok {
			anchorKind = d.ID
		}
		if b, ok := s.Apps[anchorKind]; ok {
			if _, visible := v.Blocks()[b.BlockID]; visible {
				anchor = b.BlockID
				found = true
			}
		}
		if !found {
			anchor, direction, side, ratio = main, "horizontal", "after", .7
		}
	}
	var created rex.Created
	err = m.Rex.Call(ctx, "session.new_split", map[string]any{"session_id": s.Context.Session, "anchor_id": anchor, "direction": direction, "side": side, "ratio": ratio, "focus": false, "layout": rex.AppLeaf(label, append(argv, "--tui", "--context-file", ctxPath), s.Context.Root)}, &created)
	if err != nil {
		return Result{}, err
	}
	if len(created.BlockIDs) != 1 {
		return Result{}, errors.New("Rex returned an ambiguous app creation; inspect pending label before retrying")
	}
	bid := created.BlockIDs[0]
	s.Pending.BlockID = bid
	if err = storage.WriteJSON(path, s); err != nil {
		return Result{}, err
	}
	inv, err := m.inventory(ctx, s.Context.Session)
	if err != nil {
		return Result{}, err
	}
	b, ok := inv[bid]
	if !ok || b.HostID != i.HostID || b.Label != label {
		return Result{}, errors.New("created app ownership did not match; preserved")
	}
	binding, err := m.capture(ctx, s.Context.Session, b, exe)
	if err != nil {
		return Result{}, err
	}
	s.Apps[kind] = binding
	s.Pending = nil
	after, err := m.Rex.View(ctx, s.Context.Session)
	if err != nil {
		return Result{}, err
	}
	s.LastShape = shape(s, after)
	if err = storage.WriteJSON(path, s); err != nil {
		return Result{}, err
	}
	err = m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": s.Context.Session, "block_id": bid}, nil)
	return Result{"complete", bid, true, false}, err
}
