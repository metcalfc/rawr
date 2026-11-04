package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type safetyRPC struct {
	t                            *testing.T
	identity                     rex.Identity
	view                         rex.View
	blocks                       map[string]rex.Block
	process                      map[string]rex.Process
	calls, closed                []string
	createError, malformedCreate bool
	failCloseAt                  int
	onCreate, onClose            func(map[string]any)
}

func safetyRaw(v any) []byte { b, _ := json.Marshal(v); return b }
func safetyArg(args []string, key string) string {
	for i, v := range args {
		if v == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func (f *safetyRPC) run(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
	if safetyArg(args, "whoami") == "--json" {
		f.calls = append(f.calls, "identity")
		return safetyRaw(f.identity), nil, nil
	}
	if safetyArg(args, "block") == "call" {
		bid := safetyArg(args, "-b")
		f.calls = append(f.calls, "process:"+bid)
		return safetyRaw(f.process[bid]), nil, nil
	}
	method := safetyArg(args, "call")
	var p map[string]any
	for i, v := range args {
		if v == "call" && i+2 < len(args) {
			if err := json.Unmarshal([]byte(args[i+2]), &p); err != nil {
				f.t.Fatal(err)
			}
			break
		}
	}
	f.calls = append(f.calls, method)
	switch method {
	case "session.create":
		if f.onCreate != nil {
			f.onCreate(p)
		}
		if f.createError {
			return nil, []byte("ambiguous creation timeout"), errors.New("timeout")
		}
		windows := p["initial_windows"].([]any)
		block := windows[0].(map[string]any)["layout"].(map[string]any)["block"].(map[string]any)
		f.view.Label = p["label"].(string)
		for wi := range f.view.Windows {
			for li := range f.view.Windows[wi].Layers {
				for bi := range f.view.Windows[wi].Layers[li].Blocks {
					b := &f.view.Windows[wi].Layers[li].Blocks[bi]
					if b.ID == "main" {
						b.Label = block["label"].(string)
					}
				}
			}
		}
		created := rex.Created{SessionID: f.view.ID, InitialWindows: []rex.Created{{WindowID: "window:test", BlockIDs: []string{"main"}}}}
		if f.malformedCreate {
			created.InitialWindows = nil
		}
		return safetyRaw(created), nil, nil
	case "session.view":
		return safetyRaw(f.view), nil, nil
	case "session.list_blocks":
		blocks := []rex.Block{}
		for _, b := range f.blocks {
			blocks = append(blocks, b)
		}
		return safetyRaw(map[string]any{"blocks": blocks}), nil, nil
	case "block.close":
		if f.onClose != nil {
			f.onClose(p)
		}
		f.closed = append(f.closed, p["block_id"].(string))
		if len(f.closed) == f.failCloseAt {
			return nil, []byte("ambiguous close"), errors.New("timeout")
		}
		return safetyRaw(map[string]any{}), nil, nil
	default:
		f.t.Fatalf("unexpected native action: %s %v", method, p)
		return nil, nil, nil
	}
}
func safetyFixture(t *testing.T) (*rex.Client, location, string, map[string]rawrkit.Binding, *safetyRPC) {
	t.Helper()
	base := t.TempDir()
	loc := location{Root: filepath.Join(base, "project"), State: filepath.Join(base, "state"), Bin: filepath.Join(base, "bin"), Session: "session:test", Anchor: "main", Host: "host:test", Generation: "generation:test", Version: rex.TestedVersion, Endpoint: "unix:/tmp/fake-rex", SessionLabel: "fixture-session", AnchorLabel: "fixture-main"}
	if err := os.MkdirAll(loc.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(loc.Bin, 0700); err != nil {
		t.Fatal(err)
	}
	f := &safetyRPC{t: t, identity: rex.Identity{HostID: loc.Host, InstanceID: loc.Generation, Version: loc.Version, Endpoint: loc.Endpoint}, blocks: map[string]rex.Block{}, process: map[string]rex.Process{}}
	bindings := map[string]rawrkit.Binding{}
	blocks := []rex.Block{{ID: "main", Label: loc.AnchorLabel, HostID: loc.Host}}
	for i, kind := range fixtureApps {
		path := filepath.Join(loc.Bin, kind)
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
		bid := "block:" + kind
		binding := rawrkit.Binding{BlockID: bid, Label: "owned:" + kind, PID: 100 + i, Path: path}
		bindings[kind] = binding
		b := rex.Block{ID: bid, Label: binding.Label, HostID: loc.Host}
		f.blocks[bid] = b
		blocks = append(blocks, b)
		child := rex.ProcessInfo{PID: binding.PID, Path: path}
		fg := child
		f.process[bid] = rex.Process{Child: &child, Foreground: &fg}
	}
	f.view = rex.View{ID: loc.Session, Label: loc.SessionLabel, Revision: 1, Windows: []rex.Window{{ID: "window:test", Layers: []rex.Layer{{ID: "layer:test", Kind: "tiled", Layout: &rex.LayoutNode{BlockID: "main"}, Blocks: blocks}}}}}
	r := rex.New("fake-rex", "", "")
	r.Runner = f.run
	return r, loc, filepath.Join(base, "location.json"), bindings, f
}
func TestSetupCreationFailureIsJournaledAndRetryRefused(t *testing.T) {
	r, loc, path, _, f := safetyFixture(t)
	f.createError = true
	loc.Session, loc.Anchor = "", ""
	shell := "/fixture/literal ; $(never)/bash"
	f.onCreate = func(p map[string]any) {
		var saved location
		if err := storage.ReadJSON(path, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Pending != "create-session" || saved.Host != f.identity.HostID || saved.Session != "" {
			t.Fatal(saved)
		}
		block := p["initial_windows"].([]any)[0].(map[string]any)["layout"].(map[string]any)["block"].(map[string]any)
		options := block["options"].(map[string]any)
		if options["shell"] != "none" || options["login"] != false {
			t.Fatal(options)
		}
		argv, ok := options["command"].([]any)
		if !ok || len(argv) != 4 || argv[0] != shell || argv[1] != "--noprofile" || argv[2] != "--norc" || argv[3] != "-i" {
			t.Fatal("command was not literal argv", options)
		}
	}
	if err := setupFixture(context.Background(), r, &loc, path, shell, func(context.Context) error { t.Fatal("opened apps after ambiguous create"); return nil }); err == nil {
		t.Fatal("wanted failure")
	}
	var saved location
	if err := storage.ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Pending != "create-session" || saved.Failure == "" {
		t.Fatal(saved)
	}
	calls := len(f.calls)
	if err := setupFixture(context.Background(), r, &loc, path, "/bin/bash", func(context.Context) error { return nil }); err == nil || len(f.calls) != calls {
		t.Fatal("retried an uncertain creation", err, f.calls)
	}
}
func TestSetupPersistsReturnedIDsBeforeAppOpenAndKeepsFailure(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "app-open-error"
		if malformed {
			name = "malformed-create-response"
		}
		t.Run(name, func(t *testing.T) {
			r, loc, path, _, f := safetyFixture(t)
			loc.Session, loc.Anchor = "", ""
			f.malformedCreate = malformed
			err := setupFixture(context.Background(), r, &loc, path, "/bin/bash", func(context.Context) error {
				var saved location
				if err := storage.ReadJSON(path, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Session != "session:test" || saved.Anchor != "main" || saved.Pending != "open-apps" {
					t.Fatal(saved)
				}
				return errors.New("app open failed")
			})
			if err == nil {
				t.Fatal("wanted failure")
			}
			var saved location
			if err = storage.ReadJSON(path, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Session != "session:test" || saved.Pending == "" || saved.Failure == "" {
				t.Fatal(saved)
			}
			calls := len(f.calls)
			if err = setupFixture(context.Background(), r, &loc, path, "/bin/bash", func(context.Context) error { return nil }); err == nil || len(f.calls) != calls {
				t.Fatal("retried pending setup", err)
			}
		})
	}
}
func TestReloadPreflightsEveryBindingBeforeAnyClose(t *testing.T) {
	cases := []string{"missing-binding", "missing-native", "label", "host", "pid", "path", "unknown-foreground", "busy-foreground", "ended", "generation", "version", "session-label"}
	for _, which := range cases {
		t.Run(which, func(t *testing.T) {
			r, loc, path, bindings, f := safetyFixture(t)
			kind := fixtureApps[len(fixtureApps)-1]
			b := bindings[kind]
			p := f.process[b.BlockID]
			switch which {
			case "missing-binding":
				delete(bindings, kind)
			case "missing-native":
				delete(f.blocks, b.BlockID)
			case "label":
				entry := f.blocks[b.BlockID]
				entry.Label = "user work"
				f.blocks[b.BlockID] = entry
			case "host":
				entry := f.blocks[b.BlockID]
				entry.HostID = "other"
				f.blocks[b.BlockID] = entry
			case "pid":
				p.Child.PID++
			case "path":
				p.Child.Path = "/bin/sh"
			case "unknown-foreground":
				p.Foreground = nil
			case "busy-foreground":
				p.Foreground = &rex.ProcessInfo{PID: 999, Path: "/usr/bin/vim"}
			case "ended":
				p.Child = nil
				p.Foreground = nil
				p.LastExit = json.RawMessage(`{"code":0}`)
			case "generation":
				f.identity.InstanceID = "other"
			case "version":
				f.identity.Version = "untested"
			case "session-label":
				f.view.Label = "user workspace"
			}
			f.process[b.BlockID] = p
			err := reloadFixture(context.Background(), r, &loc, path, bindings, func(context.Context) (map[string]rawrkit.Binding, error) {
				t.Fatal("reopened before complete verification")
				return nil, nil
			})
			if err == nil || len(f.closed) != 0 {
				t.Fatal("closed changed or unknown work", which, err, f.closed)
			}
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preflight mutated journal", err)
			}
		})
	}
}
func TestReloadJournalPrecedesCloseAndOnlyFixtureAppsClose(t *testing.T) {
	r, loc, path, bindings, f := safetyFixture(t)
	bindings["unrelated"] = rawrkit.Binding{BlockID: "user-work"}
	f.onClose = func(p map[string]any) {
		var saved location
		if err := storage.ReadJSON(path, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Pending != "reload-apps" || len(saved.PendingBlocks) != 3 {
			t.Fatal(saved)
		}
		processReads := 0
		for _, call := range f.calls {
			if strings.HasPrefix(call, "process:") {
				processReads++
			}
		}
		if processReads != 3 || p["session_id"] != loc.Session || p["block_id"] == "main" || p["block_id"] == "user-work" {
			t.Fatal(f.calls, p)
		}
	}
	if err := reloadFixture(context.Background(), r, &loc, path, bindings, func(context.Context) (map[string]rawrkit.Binding, error) { return bindings, nil }); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 3 {
		t.Fatal(f.closed)
	}
	var saved location
	if err := storage.ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Pending != "" || saved.Failure != "" {
		t.Fatal(saved)
	}
}
func TestReloadFailureRetainsJournalAndCannotRetryClosing(t *testing.T) {
	r, loc, path, bindings, f := safetyFixture(t)
	f.failCloseAt = 1
	if err := reloadFixture(context.Background(), r, &loc, path, bindings, func(context.Context) (map[string]rawrkit.Binding, error) {
		t.Fatal("opened after ambiguous close")
		return nil, nil
	}); err == nil {
		t.Fatal("wanted failure")
	}
	var saved location
	if err := storage.ReadJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Pending != "reload-apps" || saved.Failure == "" || len(saved.PendingBlocks) != 3 {
		t.Fatal(saved)
	}
	calls := len(f.calls)
	if err := reloadFixture(context.Background(), r, &saved, path, bindings, func(context.Context) (map[string]rawrkit.Binding, error) { return nil, nil }); err == nil || len(f.calls) != calls || len(f.closed) != 1 {
		t.Fatal("retried uncertain close", err, f.closed)
	}
}
