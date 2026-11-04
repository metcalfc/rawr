package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/model"
)

func TestOldRawrAppArgumentsCanonicalize(t *testing.T) {
	for old, want := range map[string]string{"ghrex": "ghrawr", "filerex": "filerawr", "exerex": "exerawr"} {
		got, err := appName(old)
		if err != nil || got != want {
			t.Fatalf("%s resolved to %s: %v", old, got, err)
		}
	}
}

func TestFlagsAfterCommandAndLiteralSeparator(t *testing.T) {
	o, positional, err := parse("rawr", []string{"save", "demo", "--expected-revision", "3", "--state-dir=/tmp/state", "--", "--literal"}, io.Discard)
	if err != nil || o.expectedRevision != 3 || o.stateDir != "/tmp/state" || strings.Join(positional, "|") != "save|demo|--literal" {
		t.Fatalf("parse returned %+v, %v, %v", o, positional, err)
	}
	for _, args := range [][]string{{"--toggle", "--hide"}, {"--tui", "--status"}, {"--local", "--toggle"}, {"--root"}, {"--unknown"}, {"--context-file", "x"}, {"--expected-revision=-2"}} {
		if _, _, err := parse("filerawr", args, io.Discard); err == nil {
			t.Errorf("accepted invalid args %v", args)
		}
	}
}

func TestContextCanonicalRootStableScopeAndSessionIsolation(t *testing.T) {
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "original-session")
	t.Setenv("REX_BLOCK", "original-block")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	o, _, err := parse("filerawr", []string{"--root", root}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c, err := makeContext(context.Background(), o)
	if err != nil || c.Root != canonical || c.Scope == "" || c.Session != "original-session" || c.Anchor != "original-block" {
		t.Fatalf("context = %+v, %v", c, err)
	}
	o, _, _ = parse("ghrawr", []string{"--root", root, "--session", "different-session"}, io.Discard)
	other, err := makeContext(context.Background(), o)
	if err != nil || other.Scope != c.Scope || other.Anchor != "" {
		t.Fatalf("different session inherited unrelated anchor: %+v, %v", other, err)
	}
}

func TestNativeContextOnlyExplicitOverrides(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "context.json")
	wanted := model.Context{Name: "saved project", Root: root, Scope: "project-id", Session: "native-session", Anchor: "native-main", Repository: "owner/repo", StateDir: t.TempDir()}
	data, _ := json.Marshal(wanted)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "unrelated-session")
	t.Setenv("REX_BLOCK", "unrelated-block")
	o, _, err := parse("ghrawr", []string{"--tui", "--context-file", path, "--repo", "other/repo"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c, err := makeContext(context.Background(), o)
	if err != nil || c.Name != wanted.Name || c.Session != wanted.Session || c.Anchor != wanted.Anchor || c.Scope != wanted.Scope || c.Repository != "other/repo" {
		t.Fatalf("context = %+v, %v", c, err)
	}
}

func TestVersionAndHelpNeedNoRexOrProjectWrites(t *testing.T) {
	for _, name := range []string{"ghrawr", "filerawr", "exerawr", "rawr"} {
		for _, flag := range []string{"--version", "--help"} {
			var out, stderr bytes.Buffer
			if code := Run(context.Background(), name, []string{flag}, &out, &stderr); code != 0 || out.Len() == 0 || stderr.Len() != 0 {
				t.Errorf("%s %s: %d, %q, %q", name, flag, code, out.String(), stderr.String())
			}
		}
	}
}

func TestRawrDefinitionPathDoesNotNeedRex(t *testing.T) {
	root, config, state := t.TempDir(), t.TempDir(), t.TempDir()
	var out, stderr bytes.Buffer
	args := []string{"create", "demo", root, "--apps=", "--config-dir", config, "--state-dir", state, "--rex", filepath.Join(t.TempDir(), "absent-rex")}
	if code := Run(context.Background(), "rawr", args, &out, &stderr); code != 0 {
		t.Fatalf("create: %d, %s", code, &stderr)
	}
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &project); err != nil || project.ID == "" {
		t.Fatalf("create output: %s; %v", &out, err)
	}
	out.Reset()
	args = []string{"show", project.ID, "--config-dir", config, "--state-dir", state}
	if code := Run(context.Background(), "rawr", args, &out, &stderr); code != 0 || !strings.Contains(out.String(), `"schema": 2`) {
		t.Fatalf("show: %d, %s, %s", code, &out, &stderr)
	}
}
