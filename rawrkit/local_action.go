package rawrkit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/metcalfc/rawr/rawrkit/rex"
)

// RunLocalAction runs an explicit checkout-affecting action only while the
// original local work is in the center at an idle known shell in the anchored
// directory. The pane-state lock prevents concurrent Rawr swaps. This is a
// foreground-process preflight, not a lock against input in another client or
// unrelated processes modifying the directory. The action must do its own
// domain checks and must not recursively call this Manager.
func (m *Manager) RunLocalAction(ctx context.Context, c Context, action func(context.Context) error) error {
	if action == nil {
		return errors.New("a local action is required")
	}
	return m.withState(ctx, c, true, func(s *State, i rex.Identity, v rex.View, _ string) error {
		if s.MainBlock != s.LocalBlock || s.Selected != "" {
			return errors.New("return original local work to the center with exerawr --local before checkout; current work preserved")
		}
		b, ok := v.Blocks()[s.MainBlock]
		if !ok || b.HostID != i.HostID {
			return errors.New("original local work is unavailable; checkout refused")
		}
		p, err := m.Rex.Process(ctx, c.Session, s.MainBlock)
		if err != nil {
			return err
		}
		if p.Child == nil || p.Foreground == nil || p.Child.PID <= 0 || p.Foreground.PID <= 0 || p.Child.Path == "" || p.Foreground.Path == "" {
			return errors.New("local work is busy or its foreground program is unknown; return to an idle shell before checkout")
		}
		idle := p.Foreground.PID == p.Child.PID && samePath(p.Child.Path, p.Foreground.Path)
		// Rex's macOS login terminals own /usr/bin/login, with the actual
		// idle shell as its single descendant. A nested shell is ambiguous.
		if !idle && p.Child.Path == "/usr/bin/login" && len(p.Ancestors) == 1 {
			idle = p.Ancestors[0].PID == p.Child.PID && samePath(p.Ancestors[0].Path, p.Child.Path)
		}
		if !idle {
			return errors.New("local work is busy or nested; return to the original idle shell before checkout")
		}
		switch filepath.Base(p.Foreground.Path) {
		case "sh", "bash", "zsh", "fish", "dash", "ksh", "nu":
		default:
			return errors.New("local work is not a recognized idle shell; checkout refused")
		}
		root, err := filepath.EvalSymlinks(c.Root)
		if err != nil {
			return err
		}
		cwd, err := filepath.EvalSymlinks(p.Foreground.CWD)
		if err != nil || !samePath(root, cwd) {
			return errors.New("local shell must be in the anchored project root before checkout")
		}
		var modes struct {
			Screen string `json:"screen"`
		}
		if err = m.Rex.Run(ctx, []string{"block", "call", "-s", c.Session, "-b", s.MainBlock, "modes"}, &modes); err != nil {
			return err
		}
		if modes.Screen != "primary" {
			return errors.New("local work is not a verified primary shell screen; checkout refused")
		}
		if err = action(ctx); err != nil {
			return err
		}
		if err = m.Rex.Call(ctx, "session.focus_block", map[string]any{"session_id": c.Session, "block_id": s.MainBlock}, nil); err != nil {
			return fmt.Errorf("local action completed, but focusing work failed: %w", err)
		}
		return nil
	})
}
