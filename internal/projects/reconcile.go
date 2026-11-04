package projects

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/metcalfc/rawr/rawrkit/rex"
)

func observedNode(n *rex.LayoutNode, roles map[string]string) *Node {
	if n == nil {
		return nil
	}
	if n.BlockID != "" {
		role, ok := roles[n.BlockID]
		if !ok {
			role = "extra:" + n.BlockID
		}
		return leaf(role)
	}
	if n.Split == nil {
		return nil
	}
	return split(n.Split.Direction, n.Split.Ratio, observedNode(n.Split.Before, roles), observedNode(n.Split.After, roles))
}
func equivalent(a, b *Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Role != "" || b.Role != "" {
		return a.Role == b.Role && a.Direction == b.Direction
	}
	return a.Direction == b.Direction && math.Abs(a.Ratio-b.Ratio) <= .005 && equivalent(a.Before, b.Before) && equivalent(a.After, b.After)
}
func layoutMatches(p Project, view rex.View, good map[string]string) bool {
	if len(p.Setup) != len(view.Windows) {
		return false
	}
	inverse := map[string]string{}
	for role, bid := range good {
		inverse[bid] = role
	}
	for index, window := range view.Windows {
		if len(window.Layers) != 1 || window.Layers[0].Kind != "tiled" || !equivalent(p.Setup[index].Layout, observedNode(window.Layers[0].Layout, inverse)) {
			return false
		}
	}
	return true
}

func (m *Manager) Save(ctx context.Context, id string, expected *int) (Result, error) {
	p, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	var out Result
	err = m.lock(p.ID, func() error {
		p, err = m.Get(p.ID)
		if err != nil {
			return err
		}
		if expected != nil && p.Revision != *expected {
			return fmt.Errorf("stale save revision: expected %d, found %d", *expected, p.Revision)
		}
		i, o, view, err := m.snapshot(ctx, p)
		if err != nil {
			return err
		}
		if view == nil {
			return fmt.Errorf("project is closed; open it before saving")
		}
		paneState, err := m.paneSnapshot(ctx, p, &o, i, *view)
		if err != nil {
			return err
		}
		// Only a VM explicitly selected through the shared pane layer gains a
		// relaunch recipe. Arbitrary user terminals remain shell placeholders.
		if paneState.Restarted {
			p.Roles["work-main"] = Recipe{Kind: "shell", Argv: []string{}, Cwd: p.Root, Host: "local", Reopen: "shell-placeholder"}
			b := o.Roles["work-main"]
			o.Roles["work-main"] = m.observeBinding(ctx, p, "work-main", b.BlockID, b.Label, i, o.SessionID)
		} else if paneState.SelectedTerminal != nil {
			spec := paneState.SelectedTerminal
			kind := "command"
			if spec.Metadata["provider"] == "exe.dev" {
				kind = "vm-shell"
			}
			cwd := spec.Cwd
			if cwd == "" {
				cwd = p.Root
			}
			p.Roles["work-main"] = Recipe{Kind: kind, Argv: append([]string(nil), spec.Argv...), Cwd: cwd, Host: "local", Reopen: "explicit"}
		} else if paneState.MainBlock != "" && o.Roles["work-main"].RecipeKind == "shell" && (p.Roles["work-main"].Kind == "vm-shell" || p.Roles["work-main"].Kind == "command") {
			p.Roles["work-main"] = Recipe{Kind: "shell", Argv: []string{}, Cwd: p.Root, Host: "local", Reopen: "shell-placeholder"}
		}
		blocks := view.Blocks()
		inverse := map[string]string{}
		for role, b := range o.Roles {
			if _, declared := p.Roles[role]; declared {
				if ok, _ := m.inspectBinding(ctx, p, role, b, i, o.SessionID, blocks); ok {
					inverse[b.BlockID] = role
				}
			}
		}
		candidate := p
		candidate.Roles = map[string]Recipe{}
		for role, r := range p.Roles {
			candidate.Roles[role] = r
		}
		candidate.Setup = nil
		notices := []string{}
		var node func(*rex.LayoutNode) (*Node, error)
		node = func(n *rex.LayoutNode) (*Node, error) {
			if n == nil {
				return nil, fmt.Errorf("cannot save empty native layout")
			}
			if n.BlockID != "" {
				role, ok := inverse[n.BlockID]
				if !ok {
					token, err := randomID()
					if err != nil {
						return nil, err
					}
					role = "shell-" + token[:8]
					for candidate.Roles[role].Kind != "" {
						token, err = randomID()
						if err != nil {
							return nil, err
						}
						role = "shell-" + token[:8]
					}
					candidate.Roles[role] = Recipe{Kind: "shell", Argv: []string{}, Cwd: p.Root, Host: "local", Reopen: "shell-placeholder"}
					block, ok := blocks[n.BlockID]
					if !ok {
						return nil, fmt.Errorf("native layout references an unknown surface")
					}
					o.Roles[role] = m.observeBinding(ctx, candidate, role, n.BlockID, block.Label, i, o.SessionID)
					inverse[n.BlockID] = role
					notices = append(notices, role+": shell placeholder; the current process/conversation is retained live and will not be recreated")
				}
				return leaf(role), nil
			}
			if n.Split == nil {
				return nil, fmt.Errorf("cannot save malformed native layout")
			}
			before, err := node(n.Split.Before)
			if err != nil {
				return nil, err
			}
			after, err := node(n.Split.After)
			if err != nil {
				return nil, err
			}
			return split(n.Split.Direction, n.Split.Ratio, before, after), nil
		}
		for _, window := range view.Windows {
			if len(window.Layers) != 1 || window.Layers[0].Kind != "tiled" {
				return fmt.Errorf("saving floating or empty layers is unsupported; previous setup retained")
			}
			layout, err := node(window.Layers[0].Layout)
			if err != nil {
				return err
			}
			candidate.Setup = append(candidate.Setup, Tab{Name: window.Label, Layout: layout})
		}
		candidate.Revision++
		if err = Validate(candidate); err != nil {
			return err
		}
		// Process reads may take time. Refuse to commit a layout that changed meanwhile.
		fresh, err := m.Rex.View(ctx, o.SessionID)
		if err != nil {
			return err
		}
		if fresh.Revision != view.Revision {
			return fmt.Errorf("Rex layout changed during save; retry once the arrangement is stable")
		}
		if err = m.write(candidate, p.Revision); err != nil {
			return err
		}
		rememberFocus(candidate, &o, *view)
		if err = m.record(candidate, o); err != nil {
			return err
		}
		out = result(candidate, o)
		out.LayoutMatches = true
		out.Notices = notices
		out.Notices = append(out.Notices, "Saved topology and declared recipes only; terminal processes, output and conversations are not snapshots.")
		return nil
	})
	return out, err
}

type restorePlan struct {
	Result    Result
	Identity  rex.Identity
	Observed  observed
	View      *rex.View
	Inventory map[string]rex.Block
	Good      map[string]string
	Missing   []string
	Detached  []string
	Restarted bool
}

func (m *Manager) makePlan(ctx context.Context, p Project) (restorePlan, error) {
	i, o, view, err := m.snapshot(ctx, p)
	plan := restorePlan{Identity: i, Observed: o, View: view, Good: map[string]string{}, Inventory: map[string]rex.Block{}, Result: result(p, o)}
	if err != nil {
		return plan, err
	}
	if view == nil {
		plan.Result.Actions = []string{"create saved project session"}
		return plan, nil
	}
	paneState, err := m.paneSnapshot(ctx, p, &o, i, *view)
	if err != nil {
		return plan, err
	}
	plan.Restarted = paneState.Restarted
	if plan.Restarted {
		plan.Result.Notices = append(plan.Result.Notices, "Rex retained native surfaces with fresh shells; program and transcript restoration is unsupported.")
	}
	plan.Observed = o
	all, err := m.Rex.Inventory(ctx, o.SessionID)
	if err != nil {
		return plan, err
	}
	for _, b := range all {
		plan.Inventory[b.ID] = b
	}
	visible := view.Blocks()
	for _, role := range rolesInSetup(p) {
		b, known := o.Roles[role]
		if !known {
			plan.Missing = append(plan.Missing, role)
			continue
		}
		good, why := m.inspectBinding(ctx, p, role, b, i, o.SessionID, plan.Inventory)
		if good {
			plan.Good[role] = b.BlockID
			if _, shown := visible[b.BlockID]; !shown {
				plan.Detached = append(plan.Detached, role)
			}
		} else if why == "missing" {
			plan.Missing = append(plan.Missing, role)
		} else {
			plan.Result.Conflicts = append(plan.Result.Conflicts, occupiedMessage(role, why))
		}
	}
	wanted := map[string]bool{}
	for _, bid := range plan.Good {
		wanted[bid] = true
	}
	extra := []string{}
	for bid := range visible {
		if !wanted[bid] {
			extra = append(extra, bid)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		plan.Result.Conflicts = append(plan.Result.Conflicts, fmt.Sprintf("preserve %d extra or occupied live panes; exact geometry remains unapplied", len(extra)))
	}
	for _, w := range view.Windows {
		if len(w.Layers) != 1 || w.Layers[0].Kind != "tiled" {
			plan.Result.Conflicts = append(plan.Result.Conflicts, "live floating or empty layers retained; exact tiled restore is unsupported")
		}
	}
	windowGroups := map[string]bool{}
	for _, tab := range p.Setup {
		groups := map[string]bool{}
		for _, role := range Leaves(tab.Layout) {
			if b, ok := visible[plan.Good[role]]; ok {
				groups[b.WindowID] = true
			}
		}
		if len(groups) > 1 {
			plan.Result.Conflicts = append(plan.Result.Conflicts, "saved roles span multiple live tabs; current grouping retained")
		}
		for group := range groups {
			if windowGroups[group] {
				plan.Result.Conflicts = append(plan.Result.Conflicts, "multiple saved tabs share one live tab; current grouping retained")
			}
			windowGroups[group] = true
		}
		if len(groups) == 0 && len(p.Setup) > 1 {
			plan.Result.Conflicts = append(plan.Result.Conflicts, "a saved tab has no visible anchor; create or reattach that tab explicitly before exact restore")
		}
	}
	plan.Result.LayoutMatches = layoutMatches(p, *view, plan.Good)
	for _, role := range plan.Detached {
		plan.Result.Actions = append(plan.Result.Actions, "reattach retained "+role)
	}
	for _, role := range plan.Missing {
		plan.Result.Actions = append(plan.Result.Actions, "launch "+role+" in a new native pane")
	}
	if len(plan.Result.Conflicts) > 0 {
		plan.Result.Result = "partial"
		plan.Result.Actions = append(plan.Result.Actions, "preserve live geometry")
	} else if !plan.Result.LayoutMatches {
		plan.Result.Actions = append(plan.Result.Actions, "restore native tiled topology and ratios")
	}
	return plan, nil
}

// Plan performs only reads, including when an interrupted-operation journal exists.
func (m *Manager) Plan(ctx context.Context, id string) (Result, error) {
	p, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	plan, err := m.makePlan(ctx, p)
	return plan.Result, err
}

func (m *Manager) launchRole(ctx context.Context, p Project, o *observed, i rex.Identity, role, anchor string) (string, error) {
	if anchor == "" {
		return "", fmt.Errorf("no unambiguous visible anchor remains; retained all live work")
	}
	if err := m.preflight(p, []string{role}); err != nil {
		return "", err
	}
	token, err := randomID()
	if err != nil {
		return "", err
	}
	label := "rawr-go:" + token[:8] + ":" + role
	o.Pending = &pending{Kind: "split", Role: role, Label: label}
	if err = m.record(p, *o); err != nil {
		return "", err
	}
	var created rex.Created
	err = m.Rex.Call(ctx, "session.new_split", map[string]any{"session_id": o.SessionID, "anchor_id": anchor, "direction": "horizontal", "side": "after", "ratio": .7, "layout": m.roleLeaf(p, role, label), "focus": false}, &created)
	if err != nil {
		return "", err
	}
	if len(created.BlockIDs) != 1 || created.BlockIDs[0] == "" {
		return "", fmt.Errorf("malformed split response; pending launch journal retained")
	}
	bid := created.BlockIDs[0]
	o.Roles[role] = binding{BlockID: bid, Label: label, InstanceID: i.InstanceID}
	if err = m.record(p, *o); err != nil {
		return bid, err
	}
	view, err := m.Rex.View(ctx, o.SessionID)
	if err != nil {
		return bid, err
	}
	b, ok := view.Blocks()[bid]
	if !ok || b.Label != label || b.HostID != i.HostID {
		return bid, fmt.Errorf("new native surface could not be verified; pending journal retained")
	}
	o.Roles[role] = m.observeBinding(ctx, p, role, bid, label, i, o.SessionID)
	o.Pending = nil
	if err = m.record(p, *o); err != nil {
		return bid, err
	}
	if isApp(p.Roles[role].Kind) {
		if err = m.adopt(ctx, p, *o, []string{role}); err != nil {
			return bid, err
		}
	}
	return bid, nil
}

func (m *Manager) Restore(ctx context.Context, id string) (Result, error) {
	p, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	var out Result
	err = m.lock(p.ID, func() error {
		p, err = m.Get(p.ID)
		if err != nil {
			return err
		}
		plan, err := m.makePlan(ctx, p)
		if err != nil {
			return err
		}
		o := plan.Observed
		if plan.View == nil {
			o, view, err := m.newView(ctx, p, plan.Identity)
			if err != nil {
				return err
			}
			out = result(p, o)
			out.Created = true
			good := map[string]string{}
			for role, b := range o.Roles {
				good[role] = b.BlockID
			}
			out.LayoutMatches = layoutMatches(p, view, good)
			return m.Rex.Select(ctx, o.SessionID, o.LastWork)
		}
		out = plan.Result
		if plan.Restarted && len(plan.Missing) > 0 {
			out.Result = "partial"
			out.Conflicts = append(out.Conflicts, "shared app scope belongs to the previous Rex generation; inspect retained surfaces before explicitly recreating missing tools")
			return nil
		}
		oldFocus := plan.View.Focused()
		rememberFocus(p, &o, *plan.View)
		if err = m.record(p, o); err != nil {
			return err
		}
		// A partial restore may add genuinely absent roles, but never replaces an
		// occupied terminal, closes work, or types relaunch text into a shell.
		for _, role := range plan.Missing {
			anchor := ""
			for _, tab := range p.Setup {
				for _, r := range Leaves(tab.Layout) {
					if r == role {
						for _, candidate := range Leaves(tab.Layout) {
							bid := plan.Good[candidate]
							if _, ok := plan.View.Blocks()[bid]; ok {
								anchor = bid
								break
							}
						}
					}
				}
			}
			if anchor == "" {
				out.Conflicts = append(out.Conflicts, role+": no visible saved-tab anchor; no launch performed")
				out.Result = "partial"
				continue
			}
			bid, err := m.launchRole(ctx, p, &o, plan.Identity, role, anchor)
			if err != nil {
				out.Result = "partial"
				out.Conflicts = append(out.Conflicts, err.Error())
				return nil
			}
			plan.Good[role] = bid
			out.Actions = append(out.Actions, "opened "+role)
		}
		// Reinspect after additions and before any geometric mutation.
		fresh, err := m.makePlan(ctx, p)
		if err != nil {
			return err
		}
		o = fresh.Observed
		if len(fresh.Result.Conflicts) > 0 || len(fresh.Missing) > 0 {
			out = fresh.Result
			out.Result = "partial"
			return nil
		}
		if fresh.Result.LayoutMatches {
			out = fresh.Result
			return nil
		}
		view := fresh.View
		good := fresh.Good
		sid := o.SessionID
		// Any interrupted detach/move leaves a durable refusal-to-retry journal.
		o.LayoutPending = true
		if err = m.record(p, o); err != nil {
			return err
		}
		fail := func(err error) error {
			out = fresh.Result
			out.Result = "partial"
			out.Conflicts = append(out.Conflicts, err.Error(), "native programs retained, including detached panes; inspect Rex block inventory before explicitly repairing the journal")
			return nil
		}
		for _, tab := range p.Setup {
			roles := Leaves(tab.Layout)
			first := ""
			for _, role := range roles {
				if _, ok := view.Blocks()[good[role]]; ok {
					first = role
					break
				}
			}
			if first == "" {
				return fail(fmt.Errorf("saved tab has no native visible anchor"))
			}
			for _, role := range roles {
				if role != first {
					if _, ok := view.Blocks()[good[role]]; ok {
						if err = m.Rex.Call(ctx, "session.detach_block", map[string]any{"session_id": sid, "block_id": good[role]}, nil); err != nil {
							return fail(err)
						}
					}
				}
			}
			// If the first desired leaf was hidden, attach it beside the retained
			// visible anchor and then detach that anchor. All programs remain alive.
			if first != roles[0] {
				if err = m.Rex.Call(ctx, "session.move_block", map[string]any{"session_id": sid, "block_id": good[roles[0]], "anchor_block_id": good[first], "direction": "horizontal", "side": "before", "ratio": .5}, nil); err != nil {
					return fail(err)
				}
				if err = m.Rex.Call(ctx, "session.detach_block", map[string]any{"session_id": sid, "block_id": good[first]}, nil); err != nil {
					return fail(err)
				}
			}
			var build func(*Node) error
			build = func(n *Node) error {
				if n.Role != "" {
					return nil
				}
				before := good[Leaves(n.Before)[0]]
				after := good[Leaves(n.After)[0]]
				if err := m.Rex.Call(ctx, "session.move_block", map[string]any{"session_id": sid, "block_id": after, "anchor_block_id": before, "direction": n.Direction, "side": "after", "ratio": n.Ratio}, nil); err != nil {
					return err
				}
				if err := build(n.Before); err != nil {
					return err
				}
				return build(n.After)
			}
			if err = build(tab.Layout); err != nil {
				return fail(err)
			}
		}
		final, err := m.Rex.View(ctx, sid)
		if err != nil {
			return fail(err)
		}
		if !layoutMatches(p, final, good) {
			return fail(fmt.Errorf("Rex returned different topology or ratios; exact restore is unverified (ensure Autobalance splits is off)"))
		}
		o.LayoutPending = false
		if err = m.record(p, o); err != nil {
			return err
		}
		out = result(p, o)
		out.LayoutMatches = true
		out.Actions = []string{"restored native tiled topology and ratios"}
		if _, ok := final.Blocks()[oldFocus]; ok {
			if err = m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": sid, "block_id": oldFocus}, nil); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
