// Prepares one owned native Rex shell for a GUI walkthrough. Resource panes are
// opened by typing the released bare commands in that shell, never by this helper.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type manifest struct {
	Schema       int    `json:"schema"`
	Created      string `json:"created"`
	Phase        string `json:"phase"`
	Failure      string `json:"failure,omitempty"`
	Root         string `json:"root"`
	TempDir      string `json:"temp_dir"`
	Bin          string `json:"bin"`
	Providers    string `json:"providers"`
	Bootstrap    string `json:"bootstrap"`
	Config       string `json:"config"`
	StateDir     string `json:"state_dir"`
	Scope        string `json:"scope"`
	Rex          string `json:"rex"`
	Server       string `json:"server"`
	Client       string `json:"client,omitempty"`
	Host         string `json:"host"`
	Generation   string `json:"generation"`
	Version      string `json:"version"`
	SessionLabel string `json:"session_label"`
	AnchorLabel  string `json:"anchor_label"`
	Session      string `json:"session,omitempty"`
	Window       string `json:"window,omitempty"`
	Anchor       string `json:"anchor,omitempty"`
	LocalPID     int    `json:"local_pid,omitempty"`
	Shell        string `json:"shell"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if isProvider(filepath.Base(self)) {
		return runProvider(ctx, self, args, os.Stdout, os.Stderr)
	}
	if len(args) == 2 && args[0] == "shell" {
		return shell(args[1])
	}
	mode := "setup"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		mode, args = args[0], args[1:]
	}
	f := flag.NewFlagSet("native-demo "+mode, flag.ContinueOnError)
	path := f.String("manifest", ".acceptance/native-demo/location.json", "local ownership journal (never overwrite/retry setup)")
	bin := f.String("bin", ".acceptance/vhs-v020/bin", "released app binaries")
	rexPath := f.String("rex", "", "verified Rex CLI executable")
	server := f.String("server", "", "local Rex Unix endpoint")
	client := f.String("client", "", "Rex GUI client ID or label")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	*path, err = filepath.Abs(*path)
	if err != nil {
		return err
	}
	switch mode {
	case "setup":
		return storage.WithLock(*path+".lock", func() error {
			if _, err := os.Stat(*path); err == nil {
				return errors.New("native demo already journaled; inspect status, no automatic retry or replacement")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			r := rex.New(*rexPath, *server, *client)
			m, err := prepare(*bin, self, r)
			if err != nil {
				return err
			}
			if err := create(ctx, r, &m, *path); err != nil {
				return err
			}
			return printJSON(os.Stdout, m)
		})
	case "status", "focus":
		var m manifest
		if err := storage.ReadJSON(*path, &m); err != nil {
			return err
		}
		if m.Phase != "ready" || m.Failure != "" {
			if mode == "status" {
				_ = printJSON(os.Stdout, m)
			}
			return errors.New("native demo setup is incomplete; inspect the journal, no automatic retry or focus")
		}
		if *rexPath != "" || *server != "" || *client != "" {
			return errors.New("status/focus use only the manifest's recorded Rex target")
		}
		r := rex.New(m.Rex, m.Server, m.Client)
		v, err := verify(ctx, r, m)
		if err != nil {
			return err
		}
		if mode == "focus" {
			return r.Select(ctx, m.Session, m.Anchor)
		}
		c := rawrkit.Context{Name: "rawr", Root: m.Root, Scope: m.Scope, Session: m.Session, Anchor: m.Anchor, StateDir: m.StateDir, Rex: m.Rex, Server: m.Server, Client: m.Client}
		s, err := catalog.NewManager(r, m.StateDir, m.Bin).Snapshot(ctx, c)
		if err != nil {
			return err
		}
		return printJSON(os.Stdout, struct {
			Manifest manifest                   `json:"manifest"`
			View     rex.View                   `json:"view"`
			Panes    rawrkit.Snapshot           `json:"panes"`
			Bindings map[string]rawrkit.Binding `json:"bindings"`
		}{m, v, s, s.Bindings})
	default:
		return errors.New("use setup, status or focus; no cleanup/close command is provided")
	}
}

func create(ctx context.Context, r *rex.Client, m *manifest, path string) (err error) {
	if _, err := os.Stat(path); err == nil {
		return errors.New("native demo journal exists; no automatic setup retry")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	i, err := r.Identity(ctx)
	if err != nil {
		return err
	}
	sessions, err := r.Sessions(ctx)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Label == m.SessionLabel {
			return errors.New("a native-pane-demo session already exists; review it instead of creating another")
		}
	}
	m.Host, m.Generation, m.Version, m.Server = i.HostID, i.InstanceID, i.Version, i.Endpoint
	m.Phase, m.Failure = "create-session", ""
	if err = storage.WriteJSON(path, *m); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			m.Failure = err.Error()
			_ = storage.WriteJSON(path, *m)
		}
	}()
	layout := rex.Leaf(m.AnchorLabel, []string{m.Bootstrap, "shell", m.Config}, m.Root)
	options := layout["block"].(map[string]any)["options"].(map[string]any)
	options["path_prepend"] = []string{m.Providers, m.Bin, "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	var made rex.Created
	if err = r.Call(ctx, "session.create", map[string]any{"label": m.SessionLabel, "initial_windows": []any{map[string]any{"window_label": "Rawr · native panes", "layout": layout}}}, &made); err != nil {
		return err
	}
	m.Session = made.SessionID
	if err = storage.WriteJSON(path, *m); err != nil {
		return err
	}
	if made.SessionID == "" || len(made.InitialWindows) != 1 || len(made.InitialWindows[0].BlockIDs) != 1 || made.InitialWindows[0].BlockIDs[0] == "" {
		return errors.New("ambiguous native creation; review journal IDs, do not retry setup")
	}
	m.Window, m.Anchor = made.InitialWindows[0].WindowID, made.InitialWindows[0].BlockIDs[0]
	m.Phase = "verify-shell"
	if err = storage.WriteJSON(path, *m); err != nil {
		return err
	}
	for attempt := 0; attempt < 40; attempt++ {
		var p rex.Process
		p, err = r.Process(ctx, m.Session, m.Anchor)
		if err != nil {
			return err
		}
		if p.Child != nil && p.Foreground != nil && samePath(p.Child.Path, m.Shell) && p.Child.PID == p.Foreground.PID && samePath(p.Foreground.Path, m.Shell) {
			m.LocalPID = p.Child.PID
			if _, err = verify(ctx, r, *m); err != nil {
				return err
			}
			m.Phase = "ready"
			return storage.WriteJSON(path, *m)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("created shell process not verified; inspect journal IDs, no automatic retry")
}

func verify(ctx context.Context, r *rex.Client, m manifest) (rex.View, error) {
	var zero rex.View
	if m.Schema != 1 || m.Session == "" || m.Anchor == "" || m.LocalPID <= 0 || m.Host == "" || m.Generation == "" {
		return zero, errors.New("incomplete native demo journal; explicit review required")
	}
	i, err := r.Identity(ctx)
	if err != nil {
		return zero, err
	}
	if i.HostID != m.Host || i.InstanceID != m.Generation || i.Version != m.Version || i.Endpoint != m.Server {
		return zero, errors.New("native demo Rex identity changed; existing work preserved")
	}
	v, err := r.View(ctx, m.Session)
	if err != nil {
		return zero, err
	}
	b, ok := v.Blocks()[m.Anchor]
	if v.Label != m.SessionLabel || !ok || b.Label != m.AnchorLabel || b.HostID != m.Host {
		return zero, errors.New("native demo session/shell association changed; existing work preserved")
	}
	p, err := r.Process(ctx, m.Session, m.Anchor)
	if err != nil {
		return zero, err
	}
	if p.Child == nil || p.Child.PID != m.LocalPID || !samePath(p.Child.Path, m.Shell) {
		return zero, errors.New("native demo original shell changed; existing work preserved")
	}
	return v, nil
}

func samePath(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
