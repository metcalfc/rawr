package rawrkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type Step struct {
	Direction string   `json:"direction"`
	Side      string   `json:"side"`
	Ratio     float64  `json:"ratio"`
	Siblings  []string `json:"siblings"`
	Whole     bool     `json:"whole_layer"`
}
type Placement struct {
	WindowID string `json:"window_id"`
	Chain    []Step `json:"chain"`
}

func clean(n *rex.LayoutNode) *rex.LayoutNode {
	if n == nil {
		return nil
	}
	if n.BlockID != "" {
		return &rex.LayoutNode{BlockID: n.BlockID}
	}
	s := n.Split
	return &rex.LayoutNode{Split: &rex.Split{Direction: s.Direction, Ratio: s.Ratio, Before: clean(s.Before), After: clean(s.After)}}
}
func equivalent(a, b *rex.LayoutNode) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.BlockID != "" || b.BlockID != "" {
		return a.BlockID == b.BlockID
	}
	return a.Split.Direction == b.Split.Direction && math.Abs(a.Split.Ratio-b.Split.Ratio) < .006 && equivalent(a.Split.Before, b.Split.Before) && equivalent(a.Split.After, b.Split.After)
}
func contains(n *rex.LayoutNode, id string) bool {
	for _, v := range rex.Leaves(n) {
		if v == id {
			return true
		}
	}
	return false
}
func (s *State) ref(id string) string {
	if id == s.MainBlock {
		return "@main"
	}
	for k, b := range s.Apps {
		if b.BlockID == id {
			return "@" + k
		}
	}
	return id
}
func (s *State) resolve(ref string) string {
	if ref == "@main" {
		return s.MainBlock
	}
	for k, b := range s.Apps {
		if ref == "@"+k {
			return b.BlockID
		}
	}
	return ref
}
func shape(s *State, v rex.View) string {
	var trees []any
	var node func(*rex.LayoutNode) any
	node = func(n *rex.LayoutNode) any {
		if n == nil {
			return nil
		}
		if n.BlockID != "" {
			return s.ref(n.BlockID)
		}
		return []any{n.Split.Direction, n.Split.Ratio, node(n.Split.Before), node(n.Split.After)}
	}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			if contains(l.Layout, s.MainBlock) {
				trees = append(trees, []any{w.ID, node(l.Layout)})
			}
		}
	}
	b, _ := json.Marshal(trees)
	return string(b)
}
func placement(s *State, v rex.View, bid string) (Placement, error) {
	var walk func(*rex.LayoutNode, bool) ([]Step, bool)
	walk = func(n *rex.LayoutNode, root bool) ([]Step, bool) {
		if n == nil {
			return nil, false
		}
		if n.BlockID != "" {
			return nil, n.BlockID == bid
		}
		sp := n.Split
		for _, side := range []string{"before", "after"} {
			child, other := sp.Before, sp.After
			if side == "after" {
				child, other = sp.After, sp.Before
			}
			below, ok := walk(child, false)
			if ok {
				refs := []string{}
				for _, id := range rex.Leaves(other) {
					refs = append(refs, s.ref(id))
				}
				return append(below, Step{sp.Direction, side, sp.Ratio, refs, root}), true
			}
		}
		return nil, false
	}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			chain, ok := walk(l.Layout, true)
			if ok {
				if l.Kind != "tiled" || len(chain) == 0 {
					return Placement{}, errors.New("tool must share a tiled workspace before it can be hidden")
				}
				return Placement{w.ID, chain}, nil
			}
		}
	}
	return Placement{}, errors.New("tool is not visible")
}
func remember(s *State, v rex.View) error {
	if len(s.Hidden) == 0 {
		s.Placements = map[string]Placement{}
	}
	if len(s.Hidden) == 0 || shape(s, v) != s.LastShape {
		for kind, b := range s.Apps {
			if _, visible := v.Blocks()[b.BlockID]; visible {
				p, err := placement(s, v, b.BlockID)
				if err != nil {
					return err
				}
				s.Placements[kind] = p
			}
		}
	}
	return nil
}
func lca(n *rex.LayoutNode, wanted map[string]bool) *rex.LayoutNode {
	if n == nil {
		return nil
	}
	all := map[string]bool{}
	for _, id := range rex.Leaves(n) {
		all[id] = true
	}
	for id := range wanted {
		if !all[id] {
			return nil
		}
	}
	if n.BlockID != "" {
		return n
	}
	if found := lca(n.Split.Before, wanted); found != nil {
		return found
	}
	if found := lca(n.Split.After, wanted); found != nil {
		return found
	}
	return n
}
func insert(n, target *rex.LayoutNode, bid string, step Step) *rex.LayoutNode {
	if n == target {
		tool := &rex.LayoutNode{BlockID: bid}
		before, after := tool, clean(n)
		if step.Side == "after" {
			before, after = clean(n), tool
		}
		return &rex.LayoutNode{Split: &rex.Split{Direction: step.Direction, Ratio: step.Ratio, Before: before, After: after}}
	}
	if n.BlockID != "" {
		return clean(n)
	}
	sp := n.Split
	return &rex.LayoutNode{Split: &rex.Split{Direction: sp.Direction, Ratio: sp.Ratio, Before: insert(sp.Before, target, bid, step), After: insert(sp.After, target, bid, step)}}
}

// Rebuild rearranges only the specified native surfaces; every running program survives.
func (m *Manager) rebuild(ctx context.Context, sid string, desired *rex.LayoutNode) error {
	ids := rex.Leaves(desired)
	if len(ids) == 0 {
		return errors.New("cannot rebuild an empty layer")
	}
	for _, bid := range ids[1:] {
		if err := m.Rex.Call(ctx, "session.detach_block", map[string]any{"session_id": sid, "block_id": bid}, nil); err != nil {
			return err
		}
	}
	var build func(*rex.LayoutNode) error
	build = func(n *rex.LayoutNode) error {
		if n.BlockID != "" {
			return nil
		}
		sp := n.Split
		if err := m.Rex.Call(ctx, "session.move_block", map[string]any{"session_id": sid, "block_id": rex.Leaves(sp.After)[0], "anchor_block_id": rex.Leaves(sp.Before)[0], "direction": sp.Direction, "side": "after", "ratio": sp.Ratio}, nil); err != nil {
			return err
		}
		if err := build(sp.Before); err != nil {
			return err
		}
		return build(sp.After)
	}
	return build(desired)
}
func (m *Manager) show(ctx context.Context, s *State, i rex.Identity, v rex.View, path, kind string) (Result, error) {
	b := s.Apps[kind]
	p, ok := s.Hidden[kind]
	if !ok {
		return Result{}, errors.New("tool was detached outside these controls; inspect the retained block before changing it")
	}
	inv, err := m.inventory(ctx, s.Context.Session)
	if err != nil {
		return Result{}, err
	}
	if err = m.healthy(ctx, s, i, b, inv); err != nil {
		return Result{}, err
	}
	if inv[b.BlockID].Placement != "detached" {
		return Result{}, errors.New("retained tool placement changed; preserved")
	}
	var layer *rex.LayoutNode
	window := ""
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			if l.Kind == "tiled" && contains(l.Layout, s.MainBlock) {
				layer = l.Layout
				window = w.ID
			}
		}
	}
	if layer == nil {
		return Result{}, errors.New("main workspace is unavailable; retained tool preserved")
	}
	current := clean(layer)
	var desired *rex.LayoutNode
	for _, step := range p.Chain {
		wanted := map[string]bool{}
		for _, ref := range step.Siblings {
			bid := s.resolve(ref)
			if contains(current, bid) {
				wanted[bid] = true
			}
		}
		if len(wanted) == 0 {
			continue
		}
		target := lca(current, wanted)
		if step.Whole {
			target = current
		}
		if target != nil {
			desired = insert(current, target, b.BlockID, step)
			break
		}
	}
	if desired == nil {
		return Result{}, errors.New("tool placement is ambiguous after native workspace changes; retained process preserved")
	}
	s.Pending = &Pending{Operation: "show", BlockID: b.BlockID, Blocks: rex.Leaves(desired)}
	if err = storage.WriteJSON(path, s); err != nil {
		return Result{}, err
	}
	if err = m.Rex.Call(ctx, "session.move_block", map[string]any{"session_id": s.Context.Session, "block_id": b.BlockID, "anchor_block_id": rex.Leaves(current)[0], "direction": "horizontal", "side": "before", "ratio": .2}, nil); err != nil {
		return Result{}, err
	}
	if err = m.rebuild(ctx, s.Context.Session, desired); err != nil {
		return Result{}, err
	}
	after, err := m.Rex.View(ctx, s.Context.Session)
	if err != nil {
		return Result{}, err
	}
	var actual *rex.LayoutNode
	for _, w := range after.Windows {
		if w.ID == window {
			for _, l := range w.Layers {
				if l.Kind == "tiled" {
					actual = l.Layout
					break
				}
			}
		}
	}
	if !equivalent(desired, actual) {
		return Result{}, fmt.Errorf("native tool layout differs; turn off Rex Autobalance splits and inspect the saved operation journal at %s", path)
	}
	delete(s.Hidden, kind)
	s.Pending = nil
	s.LastShape = shape(s, after)
	if len(s.Hidden) == 0 {
		s.Placements = map[string]Placement{}
	}
	if err = storage.WriteJSON(path, s); err != nil {
		return Result{}, err
	}
	err = m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": s.Context.Session, "block_id": b.BlockID}, nil)
	return Result{"complete", b.BlockID, true, true}, err
}
