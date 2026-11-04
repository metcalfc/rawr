package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func raw(v any) []byte { b, _ := json.Marshal(v); return b }

func TestSetupJournalAndSingleShell(t *testing.T) {
	for _, fail := range []string{"", "create", "malformed"} {
		t.Run(fail, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "frames-already-exist", "location.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			m := manifest{Schema: 1, Root: base, SessionLabel: "native-pane-demo", AnchorLabel: "owned", Bootstrap: "/fixture/native-demo-shell", Config: "/fixture/provider.json", Shell: "/bin/bash"}
			i := rex.Identity{HostID: "host", InstanceID: "generation", Version: rex.TestedVersion, Endpoint: "unix:/tmp/fake"}
			created := 0
			r := rex.New("fake-rex", "", "")
			r.Runner = func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
				for j, a := range args {
					if a == "whoami" {
						return raw(i), nil, nil
					}
					if a == "block" {
						return raw(rex.Process{Child: &rex.ProcessInfo{PID: 77, Path: "/bin/bash"}, Foreground: &rex.ProcessInfo{PID: 77, Path: "/bin/bash"}}), nil, nil
					}
					if a != "call" || j+2 >= len(args) {
						continue
					}
					switch args[j+1] {
					case "session.list":
						return raw(map[string]any{"sessions": []rex.Session{}}), nil, nil
					case "session.create":
						created++
						var journal manifest
						if err := storage.ReadJSON(path, &journal); err != nil {
							t.Fatal(err)
						}
						if journal.Phase != "create-session" || journal.Host != "host" || journal.Session != "" {
							t.Fatalf("mutation lacked journal: %+v", journal)
						}
						var payload struct {
							Initial []struct {
								Layout struct {
									Block struct {
										Options struct {
											Command []string `json:"command"`
										} `json:"options"`
									} `json:"block"`
								} `json:"layout"`
							} `json:"initial_windows"`
						}
						if err := json.Unmarshal([]byte(args[j+2]), &payload); err != nil {
							t.Fatal(err)
						}
						if len(payload.Initial) != 1 || !reflect.DeepEqual(payload.Initial[0].Layout.Block.Options.Command, []string{m.Bootstrap, "shell", m.Config}) {
							t.Fatalf("expected only bootstrap shell, got %s", args[j+2])
						}
						if fail == "create" {
							return nil, []byte("uncertain timeout"), errors.New("timeout")
						}
						made := rex.Created{SessionID: "session:owned"}
						if fail != "malformed" {
							made.InitialWindows = []rex.Created{{WindowID: "window:owned", BlockIDs: []string{"block:owned"}}}
						}
						return raw(made), nil, nil
					case "session.view":
						var journal manifest
						if err := storage.ReadJSON(path, &journal); err != nil {
							t.Fatal(err)
						}
						if journal.Session != "session:owned" || journal.Anchor != "block:owned" {
							t.Fatalf("returned IDs not persisted immediately: %+v", journal)
						}
						return raw(rex.View{ID: "session:owned", Label: m.SessionLabel, Revision: 1, Windows: []rex.Window{{ID: "window:owned", Layers: []rex.Layer{{Layout: &rex.LayoutNode{BlockID: "block:owned"}, Blocks: []rex.Block{{ID: "block:owned", Label: m.AnchorLabel, HostID: i.HostID}}}}}}}), nil, nil
					default:
						t.Fatalf("unexpected native operation %s", args[j+1])
					}
				}
				t.Fatalf("unexpected invocation %v", args)
				return nil, nil, nil
			}
			err := create(context.Background(), r, &m, path)
			if (err != nil) != (fail != "") {
				t.Fatalf("create error=%v", err)
			}
			var saved manifest
			if err := storage.ReadJSON(path, &saved); err != nil {
				t.Fatal(err)
			}
			if fail == "" {
				if saved.Phase != "ready" || saved.LocalPID != 77 {
					t.Fatalf("not ready: %+v", saved)
				}
			} else {
				if saved.Failure == "" || saved.Phase == "ready" {
					t.Fatalf("failure not retained: %+v", saved)
				}
				if fail == "malformed" && saved.Session != "session:owned" {
					t.Fatal("lost returned session")
				}
			}
			if err := create(context.Background(), r, &m, path); err == nil {
				t.Fatal("automatic retry permitted")
			}
			if created != 1 {
				t.Fatalf("created %d sessions", created)
			}
		})
	}
}

func TestShellEnvironmentRetainsRexAndOriginalHomeOnly(t *testing.T) {
	c := providerConfig{TempDir: "/fixture", Providers: "/fixture/providers", Bin: "/released", Rex: "/rex", Shell: "/bin/bash", Server: "unix:/tmp/rex", Client: "lunchbox"}
	env := shellEnv(c, []string{"HOME=/original/home", "REX_SESSION=session:owned", "REX_BLOCK=block:owned", "TERM=xterm-256color", "GH_TOKEN=secret", "SSH_AUTH_SOCK=/agent", "BASH_ENV=/danger", "ENV=/danger", "PROMPT_COMMAND=bad", "GIT_DIR=/other", "PATH=/untrusted", "REX_CLIENT=wrong"})
	got := map[string]string{}
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		got[k] = v
	}
	for k, want := range map[string]string{"HOME": "/original/home", "REX_SESSION": "session:owned", "REX_BLOCK": "block:owned", "REX_CLIENT": "lunchbox", "PS1": "rawr $ ", "XDG_STATE_HOME": "/fixture/state"} {
		if got[k] != want {
			t.Fatalf("%s=%q", k, got[k])
		}
	}
	for _, k := range []string{"GH_TOKEN", "SSH_AUTH_SOCK", "BASH_ENV", "ENV", "PROMPT_COMMAND", "GIT_DIR"} {
		if _, ok := got[k]; ok {
			t.Fatalf("inherited %s", k)
		}
	}
	if !strings.HasPrefix(got["PATH"], "/fixture/providers:/released:") {
		t.Fatal("fixture providers do not resolve first")
	}
}

func TestProvidersFailClosed(t *testing.T) {
	allowed := []struct {
		name string
		args []string
	}{
		{"gh", []string{"pr", "list", "--repo", "demo/rawr-workspace", "--state", "open", "--limit", "100", "--json", "number,title,url,state"}},
		{"gh", []string{"issue", "list", "--repo", "demo/rawr-workspace", "--state", "open", "--limit", "100", "--json", "number,title,url,state"}},
		{"ssh", []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "exe.dev", "ls", "--json"}},
		{"ssh", []string{"-i", "/ignored/server-key", "custom-host", "ls", "--json"}},
	}
	for _, test := range allowed {
		data, err := providerResponse(test.name, test.args)
		if err != nil || !json.Valid([]byte(data)) {
			t.Fatalf("%s: %s %v", test.name, data, err)
		}
	}
	for _, test := range []struct {
		name string
		args []string
	}{{"gh", []string{"pr", "checkout", "42"}}, {"gh", []string{"auth", "token"}}, {"ssh", []string{"demo-api.invalid"}}, {"ssh", []string{"other-host", "exec", "ls", "--json", "extra"}}, {"open", []string{"https://example.com"}}, {"xdg-open", []string{"/tmp/file"}}} {
		if _, err := providerResponse(test.name, test.args); err == nil {
			t.Fatalf("unsupported action accepted: %+v", test)
		}
	}
}

func TestFixtureRepositoryChangesAndGitIsolation(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	base := t.TempDir()
	root := filepath.Join(base, "rawr")
	for _, dir := range []string{root, filepath.Join(root, "src"), filepath.Join(root, "docs"), filepath.Join(base, "empty")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := providerConfig{Root: root, TempDir: base, Git: git}
	t.Setenv("GIT_DIR", filepath.Join(base, "not-a-repo"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(base, "missing-config"))
	t.Setenv("GIT_CONFIG_PARAMETERS", "'commit.gpgsign=true'")
	if err := prepareRepository(c); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	if err := readGit(context.Background(), c, []string{"-C", root, "status", "--porcelain=v1"}, &out, &errOut); err != nil {
		t.Fatalf("status: %v %s", err, errOut.String())
	}
	for _, row := range []string{" M docs/PLAN.md\n", "M  src/main.go\n", "?? NOTES.md\n"} {
		if !strings.Contains(out.String(), row) {
			t.Fatalf("missing %q in %q", row, out.String())
		}
	}
	for _, args := range [][]string{{"-C", root, "fetch"}, {"-C", root, "checkout", "main"}, {"-C", base, "status", "--porcelain=v1"}, {"-C", root, "config", "--global", "user.name"}} {
		if err := allowedGit(c, args, root); err == nil {
			t.Fatalf("unsupported Git request accepted %v", args)
		}
	}
}
