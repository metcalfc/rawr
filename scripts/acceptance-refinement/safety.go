package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func recordFailure(path string, loc *location, err error) {
	if err != nil && loc.Pending != "" {
		loc.Failure = err.Error()
		_ = storage.WriteJSON(path, *loc)
	}
}

// setupFixture writes an uncertain-creation journal before the first mutation.
// A failed response or app open never permits a blind second setup.
func setupFixture(ctx context.Context, r *rex.Client, loc *location, path, shell string, openApps func(context.Context) error) (err error) {
	if _, statErr := os.Stat(path); statErr == nil {
		return errors.New("fixture workspace already recorded; inspect its journal instead of retrying setup")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	i, err := r.Identity(ctx)
	if err != nil {
		return err
	}
	loc.Host, loc.Generation, loc.Version, loc.Endpoint = i.HostID, i.InstanceID, i.Version, i.Endpoint
	loc.SessionLabel = "rawr-resource-refinement · " + filepath.Base(filepath.Dir(loc.Root))
	loc.AnchorLabel = "refinement-local:" + filepath.Base(filepath.Dir(loc.Root))
	loc.Pending = "create-session"
	loc.Failure = ""
	if err = storage.WriteJSON(path, *loc); err != nil {
		return err
	}
	defer func() { recordFailure(path, loc, err) }()
	var made rex.Created
	err = r.Call(ctx, "session.create", map[string]any{"label": loc.SessionLabel, "initial_windows": []any{map[string]any{"window_label": "Rawr · provider fixtures", "layout": rex.Leaf(loc.AnchorLabel, []string{shell, "--noprofile", "--norc", "-i"}, loc.Root)}}}, &made)
	if err != nil {
		return err
	}
	loc.Session = made.SessionID
	// Preserve even an incomplete response's returned session ID for diagnosis.
	if err = storage.WriteJSON(path, *loc); err != nil {
		return err
	}
	if made.SessionID == "" || len(made.InitialWindows) != 1 || len(made.InitialWindows[0].BlockIDs) != 1 || made.InitialWindows[0].BlockIDs[0] == "" {
		return errors.New("ambiguous fixture creation; inspect the pending session record")
	}
	loc.Anchor = made.InitialWindows[0].BlockIDs[0]
	loc.Pending = "open-apps"
	if err = storage.WriteJSON(path, *loc); err != nil {
		return err
	}
	v, err := r.View(ctx, loc.Session)
	if err != nil {
		return err
	}
	block, ok := v.Blocks()[loc.Anchor]
	if v.Label != loc.SessionLabel || !ok || block.Label != loc.AnchorLabel || block.HostID != loc.Host {
		return errors.New("created fixture identity is unverified; pending record retained")
	}
	if err = openApps(ctx); err != nil {
		return err
	}
	loc.Pending, loc.Failure = "", ""
	return storage.WriteJSON(path, *loc)
}

func sameExecutable(a, b string) bool {
	if actual, err := filepath.EvalSymlinks(a); err == nil {
		a = actual
	}
	if actual, err := filepath.EvalSymlinks(b); err == nil {
		b = actual
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// All three app processes must be verified before any fixture surface closes.
func reloadPreflight(ctx context.Context, r *rex.Client, loc location, bindings map[string]rawrkit.Binding) ([]string, error) {
	if loc.Pending != "" {
		return nil, errors.New("incomplete fixture operation; no automatic reload retry")
	}
	if loc.Host == "" || loc.Generation == "" || loc.Version == "" || loc.Endpoint == "" || loc.SessionLabel == "" {
		return nil, errors.New("fixture manifest has no complete native identity; inspect it explicitly, no automatic adoption")
	}
	i, err := r.Identity(ctx)
	if err != nil {
		return nil, err
	}
	if i.HostID != loc.Host || i.InstanceID != loc.Generation || i.Version != loc.Version || i.Endpoint != loc.Endpoint {
		return nil, errors.New("fixture Rex host, generation, version or endpoint changed; live work preserved")
	}
	v, err := r.View(ctx, loc.Session)
	if err != nil {
		return nil, err
	}
	if v.Label != loc.SessionLabel {
		return nil, errors.New("fixture session association changed; live work preserved")
	}
	list, err := r.Inventory(ctx, loc.Session)
	if err != nil {
		return nil, err
	}
	inventory := map[string]rex.Block{}
	for _, b := range list {
		inventory[b.ID] = b
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, kind := range fixtureApps {
		binding, ok := bindings[kind]
		if !ok || binding.BlockID == "" || binding.BlockID == loc.Anchor || binding.PID <= 0 || binding.Label == "" || !filepath.IsAbs(binding.Path) || seen[binding.BlockID] {
			return nil, fmt.Errorf("%s fixture binding is missing or ambiguous; nothing closed", kind)
		}
		seen[binding.BlockID] = true
		b, ok := inventory[binding.BlockID]
		if !ok || b.HostID != loc.Host || b.Label != binding.Label {
			return nil, fmt.Errorf("%s native identity changed; nothing closed", kind)
		}
		expected, err := exec.LookPath(filepath.Join(loc.Bin, kind))
		if err != nil || !sameExecutable(expected, binding.Path) {
			return nil, fmt.Errorf("%s is not the declared fixture executable; nothing closed", kind)
		}
		p, err := r.Process(ctx, loc.Session, binding.BlockID)
		if err != nil {
			return nil, err
		}
		if p.Child == nil || p.Foreground == nil || p.Child.PID != binding.PID || p.Foreground.PID != binding.PID || !sameExecutable(p.Child.Path, binding.Path) || !sameExecutable(p.Foreground.Path, binding.Path) || (len(p.LastExit) > 0 && string(p.LastExit) != "null") {
			return nil, fmt.Errorf("%s contains changed, busy, ended or unknown work; nothing closed", kind)
		}
		ids = append(ids, binding.BlockID)
	}
	return ids, nil
}

func reloadFixture(ctx context.Context, r *rex.Client, loc *location, path string, bindings map[string]rawrkit.Binding, openApps func(context.Context) (map[string]rawrkit.Binding, error)) (err error) {
	ids, err := reloadPreflight(ctx, r, *loc, bindings)
	if err != nil {
		return err
	}
	loc.Pending = "reload-apps"
	loc.PendingBlocks = append([]string(nil), ids...)
	loc.Failure = ""
	if err = storage.WriteJSON(path, *loc); err != nil {
		return err
	}
	defer func() { recordFailure(path, loc, err) }()
	for _, id := range ids {
		if err = r.Call(ctx, "block.close", map[string]any{"session_id": loc.Session, "block_id": id}, nil); err != nil {
			return err
		}
	}
	loc.Apps, err = openApps(ctx)
	if err != nil {
		return err
	}
	loc.Pending, loc.Failure = "", ""
	loc.PendingBlocks = nil
	return storage.WriteJSON(path, *loc)
}
