package projects

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func isApp(kind string) bool { return kind == "filerawr" || kind == "ghrawr" || kind == "exerawr" }

func (m *Manager) command(p Project, role string) []string {
	r := p.Roles[role]
	if !isApp(r.Kind) {
		return r.Argv
	}
	exe := r.Kind
	if m.BinaryDir != "" {
		exe = filepath.Join(m.BinaryDir, exe)
	}
	args := []string{exe, "--tui", "--scope", rawrkit.ScopeForRoot(p.Root), "--root", p.Root, "--state-dir", m.AppsDir, "--rex", m.Rex.Executable}
	if p.Repository != "" {
		args = append(args, "--repo", p.Repository)
	}
	if p.VM != "" {
		args = append(args, "--vm", p.VM)
	}
	if m.Rex.Server != "" {
		args = append(args, "--server", m.Rex.Server)
	}
	if m.Rex.ClientID != "" {
		args = append(args, "--client", m.Rex.ClientID)
	}
	return args
}
func (m *Manager) appContext(p Project, o observed) model.Context {
	return model.Context{Name: p.Name, Root: p.Root, Repository: p.Repository, VM: p.VM, Scope: rawrkit.ScopeForRoot(p.Root), Session: o.SessionID, Anchor: o.Roles["work-main"].BlockID, Rex: m.Rex.Executable, Server: m.Rex.Server, Client: m.Rex.ClientID, StateDir: m.AppsDir}
}

// Context returns an already-open, verified association for Rawr's convenience
// commands. Standalone commands construct their own context without this layer.
func (m *Manager) Context(ctx context.Context, id string) (model.Context, error) {
	p, err := m.Get(id)
	if err != nil {
		return model.Context{}, err
	}
	i, o, view, err := m.snapshot(ctx, p)
	if err != nil {
		return model.Context{}, err
	}
	if view == nil {
		return model.Context{}, fmt.Errorf("project is closed; open it first")
	}
	if _, err = m.paneSnapshot(ctx, p, &o, i, *view); err != nil {
		return model.Context{}, err
	}
	return m.appContext(p, o), nil
}

func (m *Manager) paneSnapshot(ctx context.Context, p Project, o *observed, i rex.Identity, view rex.View) (rawrkit.Snapshot, error) {
	manager := catalog.NewManager(m.Rex, m.AppsDir, m.BinaryDir)
	s, err := manager.Snapshot(ctx, m.appContext(p, *o))
	if err != nil {
		return s, err
	}
	if s.Pending != nil {
		return s, fmt.Errorf("shared pane operation is interrupted; all work retained; inspect its pending journal")
	}
	if s.MainBlock != "" && s.MainBlock != o.Roles["work-main"].BlockID {
		block, ok := view.Blocks()[s.MainBlock]
		if !ok || block.HostID != i.HostID {
			return s, fmt.Errorf("shared center pane is not visible on the verified host; retained association")
		}
		// The center changed through the shared manager's native swap, not title
		// inference. Capture its current program without relaunching anything.
		temporary := p
		temporary.Roles = map[string]Recipe{"work-main": {Kind: "shell"}}
		o.Roles["work-main"] = m.observeBinding(ctx, temporary, "work-main", block.ID, block.Label, i, o.SessionID)
		o.LastWork = block.ID
	}
	// A standalone command can explicitly recreate a closed app in this same
	// scope. Reconcile its returned ID rather than launching a second copy from
	// the project's older observation. Detached old surfaces count as live work.
	var inventory map[string]rex.Block
	for role, recipe := range p.Roles {
		if !isApp(recipe.Kind) || s.Restarted {
			continue
		}
		shared, ok := s.Bindings[recipe.Kind]
		if !ok || shared.BlockID == o.Roles[role].BlockID {
			continue
		}
		if inventory == nil {
			all, err := m.Rex.Inventory(ctx, o.SessionID)
			if err != nil {
				return s, err
			}
			inventory = map[string]rex.Block{}
			for _, block := range all {
				inventory[block.ID] = block
			}
		}
		old := o.Roles[role]
		if old.BlockID != "" {
			if _, exists := inventory[old.BlockID]; exists {
				return s, fmt.Errorf("%s: both project and shared app surfaces remain; preserved both for explicit association", role)
			}
		}
		block, exists := inventory[shared.BlockID]
		if !exists || block.HostID != i.HostID || block.Label != shared.Label {
			return s, fmt.Errorf("%s: shared app surface identity changed; preserved existing work", role)
		}
		b := binding{BlockID: shared.BlockID, Label: shared.Label, InstanceID: i.InstanceID, RecipeKind: recipe.Kind}
		proc, err := m.Rex.Process(ctx, o.SessionID, shared.BlockID)
		if err == nil && proc.Child != nil && proc.Child.PID == shared.PID && canonical(proc.Child.Path) == canonical(shared.Path) && !exited(proc) && (m.expectedProgram(p, role, *proc.Child) || legacyBoundApp(recipe.Kind, *proc.Child)) {
			child := *proc.Child
			b.PID = child.PID
			b.Program = &child
		}
		// An unverified program remains occupied (PID zero), never missing.
		o.Roles[role] = b
	}
	return s, nil
}
func (m *Manager) adopt(ctx context.Context, p Project, o observed, roles []string) error {
	manager := catalog.NewManager(m.Rex, m.AppsDir, m.BinaryDir)
	for _, role := range roles {
		if isApp(p.Roles[role].Kind) {
			if err := manager.Adopt(ctx, m.appContext(p, o), p.Roles[role].Kind, o.Roles[role].BlockID, o.Roles["work-main"].BlockID); err != nil {
				return fmt.Errorf("project opened, but shared pane registration failed for %s: %w", role, err)
			}
		}
	}
	return nil
}
func (m *Manager) preflight(p Project, roles []string) error {
	for _, role := range roles {
		r := p.Roles[role]
		info, err := os.Stat(r.Cwd)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s: working directory is inaccessible: %s", role, r.Cwd)
		}
		argv := m.command(p, role)
		if len(argv) > 0 {
			if _, err = exec.LookPath(argv[0]); err != nil {
				return fmt.Errorf("%s: executable unavailable: %s", role, argv[0])
			}
		}
	}
	return nil
}
func (m *Manager) nativeLayout(p Project, n *Node, labels map[string]string) map[string]any {
	if n.Role != "" {
		return rex.Leaf(labels[n.Role], m.command(p, n.Role), p.Roles[n.Role].Cwd)
	}
	return map[string]any{"split": map[string]any{"direction": n.Direction, "ratio": n.Ratio, "before": m.nativeLayout(p, n.Before, labels), "after": m.nativeLayout(p, n.After, labels)}}
}
func rolesInSetup(p Project) []string {
	var roles []string
	for _, tab := range p.Setup {
		roles = append(roles, Leaves(tab.Layout)...)
	}
	return roles
}
func result(p Project, o observed) Result {
	return Result{Result: "complete", ProjectID: p.ID, SessionID: o.SessionID, Revision: p.Revision}
}

func (m *Manager) snapshot(ctx context.Context, p Project) (rex.Identity, observed, *rex.View, error) {
	if err := m.checkLocation(false); err != nil {
		return rex.Identity{}, observed{}, nil, err
	}
	i, err := m.Rex.Identity(ctx)
	if err != nil {
		return i, observed{}, nil, err
	}
	o, err := m.readObserved(p)
	if err != nil {
		return i, o, nil, err
	}
	if o.Pending != nil || o.LayoutPending {
		return i, o, nil, fmt.Errorf("interrupted native operation: programs were preserved; inspect %s and Rex's native block inventory before retrying", m.statePath(p.ID))
	}
	if o.HostID != "" && o.HostID != i.HostID {
		return i, o, nil, fmt.Errorf("Rex host identity changed; existing runtime ownership was preserved")
	}
	if o.SessionID == "" {
		return i, o, nil, nil
	}
	sessions, err := m.Rex.Sessions(ctx)
	if err != nil {
		return i, o, nil, err
	}
	for _, s := range sessions {
		if s.ID == o.SessionID {
			view, err := m.Rex.View(ctx, s.ID)
			if err != nil {
				return i, o, nil, err
			}
			if view.Label != o.SessionLabel {
				return i, o, nil, fmt.Errorf("associated Rex session label changed; inspect the saved association before mutation")
			}
			return i, o, &view, nil
		}
	}
	return i, o, nil, nil
}

// inspectBinding verifies known programs by native identity. Shell recipes are
// placeholders and intentionally accept any current program without relaunching it.
func (m *Manager) inspectBinding(ctx context.Context, p Project, role string, b binding, i rex.Identity, sid string, blocks map[string]rex.Block) (bool, string) {
	block, ok := blocks[b.BlockID]
	if !ok {
		return false, "missing"
	}
	if block.HostID != i.HostID || block.Label != b.Label {
		return false, "native surface identity changed; retained"
	}
	if p.Roles[role].Kind == "shell" {
		return true, ""
	}
	if b.InstanceID != i.InstanceID {
		return false, "Rex restarted with a fresh terminal; retained"
	}
	if b.PID == 0 || b.Program == nil {
		return false, "program verification incomplete; retained"
	}
	proc, err := m.Rex.Process(ctx, sid, b.BlockID)
	if err != nil {
		return false, "program cannot be inspected; retained"
	}
	if proc.Child == nil || proc.Child.PID != b.PID || !sameProgram(*proc.Child, *b.Program) || exited(proc) {
		return false, "terminal program changed or exited; retained"
	}
	if !m.expectedProgram(p, role, *proc.Child) && !legacyBoundApp(p.Roles[role].Kind, *proc.Child) {
		return false, "different program occupies the declared surface; retained"
	}
	return true, ""
}

// This check is used only after a durable native binding's PID and full
// fingerprint (or the SDK's PID/path/label binding) have already matched. It
// never authorizes launching an alias or adopting an arbitrary terminal.
func legacyBoundApp(kind string, child rex.ProcessInfo) bool {
	alias := legacyAppName(kind)
	if alias == "" {
		return false
	}
	return filepath.IsAbs(child.Path) && (filepath.Base(child.Path) == alias || filepath.Base(child.Path) == alias+".exe")
}
func sameProgram(a, b rex.ProcessInfo) bool {
	a.CWD = ""
	b.CWD = ""
	return reflect.DeepEqual(a, b)
}
func exited(p rex.Process) bool { return len(p.LastExit) > 0 && string(p.LastExit) != "null" }
func canonical(path string) string {
	if path == "" {
		return ""
	}
	if found, err := exec.LookPath(path); err == nil {
		path = found
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
func (m *Manager) expectedProgram(p Project, role string, child rex.ProcessInfo) bool {
	argv := m.command(p, role)
	if len(argv) == 0 {
		return true
	}
	wanted := canonical(argv[0])
	for _, candidate := range []string{child.Path, child.InvokedPath, child.Argv0} {
		if candidate != "" && canonical(candidate) == wanted {
			return true
		}
	}
	return false
}
func (m *Manager) observeBinding(ctx context.Context, p Project, role, bid, label string, i rex.Identity, sid string) binding {
	b := binding{BlockID: bid, Label: label, InstanceID: i.InstanceID, RecipeKind: p.Roles[role].Kind}
	// Observe once per attempt and record the same fingerprint we verified.
	for attempt := 0; attempt < 12; attempt++ {
		proc, err := m.Rex.Process(ctx, sid, bid)
		if err == nil && proc.Child != nil && proc.Child.PID > 0 && !exited(proc) && (p.Roles[role].Kind == "shell" || m.expectedProgram(p, role, *proc.Child)) {
			child := *proc.Child
			b.PID = child.PID
			b.Program = &child
			return b
		}
		if ctx.Err() != nil {
			return b
		}
		if attempt < 11 {
			timer := time.NewTimer(40 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return b
			case <-timer.C:
			}
		}
	}
	return b
}

func (m *Manager) newView(ctx context.Context, p Project, i rex.Identity) (observed, rex.View, error) {
	roles := rolesInSetup(p)
	if err := m.preflight(p, roles); err != nil {
		return observed{}, rex.View{}, err
	}
	token, err := randomID()
	if err != nil {
		return observed{}, rex.View{}, err
	}
	labels := map[string]string{}
	for _, role := range roles {
		labels[role] = "rawr-go:" + token[:8] + ":" + role
	}
	o := observed{Schema: Schema, HostID: i.HostID, InstanceID: i.InstanceID, Roles: map[string]binding{}, Pending: &pending{Kind: "session", Token: token}}
	if err = m.record(p, o); err != nil {
		return o, rex.View{}, err
	}
	label := p.Name + " [rawr-go:" + p.ID[:8] + ":" + token[:8] + "]"
	windows := []map[string]any{}
	for _, tab := range p.Setup {
		windows = append(windows, map[string]any{"window_label": tab.Name, "layout": m.nativeLayout(p, tab.Layout, labels)})
	}
	var created rex.Created
	if err = m.Rex.Call(ctx, "session.create", map[string]any{"label": label, "initial_windows": windows}, &created); err != nil {
		return o, rex.View{}, err
	}
	if created.SessionID == "" {
		return o, rex.View{}, fmt.Errorf("malformed Rex session creation; pending journal retained, do not retry blindly")
	}
	o.SessionID = created.SessionID
	o.SessionLabel = label
	ids := []string{}
	for _, window := range created.InitialWindows {
		ids = append(ids, window.BlockIDs...)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			_ = m.record(p, o)
			return o, rex.View{}, fmt.Errorf("malformed Rex creation block IDs; pending journal retained")
		}
		seen[id] = true
	}
	if len(ids) != len(roles) {
		_ = m.record(p, o)
		return o, rex.View{}, fmt.Errorf("incomplete Rex creation block IDs; pending journal retained")
	}
	for index, role := range roles {
		o.Roles[role] = binding{BlockID: ids[index], Label: labels[role], InstanceID: i.InstanceID}
	}
	// Returned IDs are durable before process inspection; no uncertain launch is retried.
	if err = m.record(p, o); err != nil {
		return o, rex.View{}, err
	}
	view, err := m.Rex.View(ctx, o.SessionID)
	if err != nil {
		return o, view, err
	}
	blocks := view.Blocks()
	for _, role := range roles {
		b := o.Roles[role]
		block, ok := blocks[b.BlockID]
		if !ok || block.Label != b.Label || block.HostID != i.HostID {
			return o, view, fmt.Errorf("created surface identity could not be verified; pending journal retained")
		}
	}
	o.Pending = nil
	for _, role := range roles {
		b := o.Roles[role]
		o.Roles[role] = m.observeBinding(ctx, p, role, b.BlockID, b.Label, i, o.SessionID)
	}
	o.LastWork = o.Roles["work-main"].BlockID
	if err = m.record(p, o); err != nil {
		return o, view, err
	}
	if err = m.adopt(ctx, p, o, roles); err != nil {
		return o, view, err
	}
	view, err = m.Rex.View(ctx, o.SessionID)
	return o, view, err
}

func rememberFocus(p Project, o *observed, view rex.View) {
	focus := view.Focused()
	for role, b := range o.Roles {
		r, ok := p.Roles[role]
		if ok && b.BlockID == focus && (r.Kind == "shell" || r.Kind == "command" || r.Kind == "vm-shell") {
			o.LastWork = focus
		}
	}
}
func (m *Manager) Open(ctx context.Context, id string) (Result, error) {
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
		i, o, view, err := m.snapshot(ctx, p)
		if err != nil {
			return err
		}
		created := view == nil
		if created {
			o, v, e := m.newView(ctx, p, i)
			if e != nil {
				return e
			}
			view = &v
			out = result(p, o)
			out.Created = true
			if err = m.Rex.Select(ctx, o.SessionID, o.LastWork); err != nil {
				return err
			}
			good := map[string]string{}
			for role, b := range o.Roles {
				good[role] = b.BlockID
			}
			out.LayoutMatches = layoutMatches(p, *view, good)
			if !out.LayoutMatches {
				out.Result = "partial"
				out.Conflicts = []string{"Rex returned a different initial layout; ensure Autobalance splits is off before exact restore"}
			}
			return nil
		}
		if _, err = m.paneSnapshot(ctx, p, &o, i, *view); err != nil {
			return err
		}
		rememberFocus(p, &o, *view)
		if err = m.record(p, o); err != nil {
			return err
		}
		out = result(p, o)
		out.Created = created
		bid := o.LastWork
		if _, ok := view.Blocks()[bid]; !ok {
			bid = view.Focused()
		}
		// Opening a live project only selects it. Hidden panes and temporary layout remain intact.
		return m.Rex.Select(ctx, o.SessionID, bid)
	})
	return out, err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func occupiedMessage(role, reason string) string { return role + ": " + strings.TrimSpace(reason) }
