package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/metcalfc/rawr/internal/storage"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type providerConfig struct {
	Root, TempDir, Bin, Providers, Git, Shell, Rex, Server, Client string
}

func printJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func prepare(bin, self string, r *rex.Client) (manifest, error) {
	var m manifest
	bin, err := filepath.Abs(bin)
	if err != nil {
		return m, err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return m, err
	}
	for _, name := range []string{"ghrawr", "filerawr", "exerawr"} {
		info, err := os.Stat(filepath.Join(bin, name))
		if err != nil {
			return m, err
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return m, fmt.Errorf("released %s executable unavailable", name)
		}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return m, err
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return m, err
	}
	base, err := os.MkdirTemp("", "rawr-native-demo-")
	if err != nil {
		return m, err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return m, err
	}
	c := providerConfig{Root: filepath.Join(base, "rawr"), TempDir: base, Bin: bin, Providers: filepath.Join(base, "providers"), Git: git, Shell: "/bin/bash", Rex: r.Executable, Server: r.Server, Client: r.ClientID}
	for _, dir := range []string{c.Root, c.Providers, filepath.Join(c.Root, "src"), filepath.Join(c.Root, "docs"), filepath.Join(base, "config"), filepath.Join(base, "state"), filepath.Join(base, "empty")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return m, err
		}
	}
	body, err := os.ReadFile(self)
	if err != nil {
		return m, err
	}
	for _, name := range []string{"gh", "ssh", "open", "xdg-open", "git"} {
		if err := os.WriteFile(filepath.Join(c.Providers, name), body, 0700); err != nil {
			return m, err
		}
	}
	bootstrap := filepath.Join(base, "native-demo-shell")
	if err := os.WriteFile(bootstrap, body, 0700); err != nil {
		return m, err
	}
	config := filepath.Join(base, "provider.json")
	if err := storage.WriteJSON(config, c); err != nil {
		return m, err
	}
	if err := prepareRepository(c); err != nil {
		return m, err
	}
	m = manifest{Schema: 1, Created: time.Now().UTC().Format(time.RFC3339), Root: c.Root, TempDir: base, Bin: bin, Providers: c.Providers, Bootstrap: bootstrap, Config: config, StateDir: filepath.Join(base, "state", "rexapps"), Scope: rawrkit.ScopeForRoot(c.Root), Rex: r.Executable, Server: r.Server, Client: r.ClientID, SessionLabel: "native-pane-demo", AnchorLabel: "native-demo-local:" + filepath.Base(base), Shell: c.Shell}
	return m, nil
}

// Git's system/global config, credential helpers, signing and hooks never enter
// this disposable repository. No fixture operation contacts its example remote.
func gitEnv(c providerConfig) []string {
	return []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "XDG_CONFIG_HOME=" + filepath.Join(c.TempDir, "config"), "LANG=C", "LC_ALL=C"}
}
func fixtureGit(c providerConfig, args ...string) error {
	argv := append([]string{"-c", "core.hooksPath=" + filepath.Join(c.TempDir, "empty"), "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command(c.Git, argv...)
	cmd.Env = gitEnv(c)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fixture git: %w: %s", err, out)
	}
	return nil
}
func prepareRepository(c providerConfig) error {
	files := map[string]string{
		"README.md":    "# Rawr native pane demo\n\nGitHub and VM data are FIXTURES. This is a disposable local repository.\n\nBare ghrawr, filerawr and exerawr open independent native Rex panes.\n",
		"src/main.go":  "package main\n\nfunc main() {}\n",
		"docs/PLAN.md": "# Demo plan\n\nBrowse resources beside your work.\n",
	}
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(c.Root, path), []byte(body), 0600); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"init", "--quiet", "--template=" + filepath.Join(c.TempDir, "empty"), "--initial-branch=demo/native-panes", c.Root}, {"-C", c.Root, "remote", "add", "origin", "https://github.com/demo/rawr-workspace.git"}, {"-C", c.Root, "add", "."}, {"-C", c.Root, "-c", "user.name=Rawr Fixture", "-c", "user.email=demo@example.invalid", "commit", "-qm", "Disposable native demo baseline"}} {
		if err := fixtureGit(c, args...); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(c.Root, "src", "main.go"), []byte("package main\n\n// FIXTURE: a staged change.\nfunc main() {}\n"), 0600); err != nil {
		return err
	}
	if err := fixtureGit(c, "-C", c.Root, "add", "src/main.go"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.Root, "docs", "PLAN.md"), []byte(files["docs/PLAN.md"]+"\nFIXTURE: an unstaged change.\n"), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.Root, "NOTES.md"), []byte("FIXTURE: an untracked file in this disposable workspace.\n"), 0600)
}

func shellEnv(c providerConfig, inherited []string) []string {
	keep := map[string]bool{"HOME": true, "REX_SESSION": true, "REX_BLOCK": true, "TERM": true, "COLORTERM": true}
	var env []string
	for _, entry := range inherited {
		key, _, ok := strings.Cut(entry, "=")
		if ok && keep[key] {
			env = append(env, entry)
		}
	}
	env = append(env, "PATH="+strings.Join([]string{c.Providers, c.Bin, "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, string(os.PathListSeparator)), "SHELL="+c.Shell, "PS1=rawr $ ", "HISTFILE=/dev/null", "BASH_SILENCE_DEPRECATION_WARNING=1", "XDG_STATE_HOME="+filepath.Join(c.TempDir, "state"), "XDG_CONFIG_HOME="+filepath.Join(c.TempDir, "config"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "RAWR_REX="+c.Rex, "LANG=en_US.UTF-8")
	if c.Server != "" {
		env = append(env, "RAWR_SERVER="+c.Server)
	}
	if c.Client != "" {
		env = append(env, "REX_CLIENT="+c.Client)
	}
	return env
}
func shell(path string) error {
	var c providerConfig
	if err := storage.ReadJSON(path, &c); err != nil {
		return err
	}
	if os.Getenv("REX_SESSION") == "" || os.Getenv("REX_BLOCK") == "" {
		return errors.New("demo shell requires Rex-injected session and block IDs")
	}
	if err := os.Chdir(c.Root); err != nil {
		return err
	}
	fmt.Print("\x1b[2J\x1b[HNative Rex workspace\nGitHub and VM data are fixtures.\n\n")
	return syscall.Exec(c.Shell, []string{c.Shell, "--noprofile", "--norc", "-i"}, shellEnv(c, os.Environ()))
}

func isProvider(name string) bool {
	return name == "gh" || name == "ssh" || name == "open" || name == "xdg-open" || name == "git"
}
func runProvider(ctx context.Context, self string, args []string, out, errOut io.Writer) error {
	name := filepath.Base(self)
	var c providerConfig
	if err := storage.ReadJSON(filepath.Join(filepath.Dir(filepath.Dir(self)), "provider.json"), &c); err != nil {
		return err
	}
	if name == "git" {
		return readGit(ctx, c, args, out, errOut)
	}
	result, err := providerResponse(name, args)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, result)
	return err
}
func providerResponse(name string, args []string) (string, error) {
	equal := func(want ...string) bool {
		if len(args) != len(want) {
			return false
		}
		for i := range args {
			if args[i] != want[i] {
				return false
			}
		}
		return true
	}
	switch name {
	case "gh":
		if equal("pr", "list", "--repo", "demo/rawr-workspace", "--state", "open", "--limit", "100", "--json", "number,title,url,state") {
			return `[{"number":42,"title":"Keep native panes beside work (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/pull/42"},{"number":41,"title":"Preserve local work (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/pull/41"}]`, nil
		}
		if equal("issue", "list", "--repo", "demo/rawr-workspace", "--state", "open", "--limit", "100", "--json", "number,title,url,state") {
			return `[{"number":17,"title":"Browse project resources (FIXTURE)","state":"OPEN","url":"https://github.com/demo/rawr-workspace/issues/17"}]`, nil
		}
		if equal("pr", "checks", "42", "--repo", "demo/rawr-workspace", "--json", "name,bucket,state,link") || equal("pr", "checks", "41", "--repo", "demo/rawr-workspace", "--json", "name,bucket,state,link") {
			return `[{"name":"Go tests (FIXTURE)","bucket":"pass","state":"SUCCESS","link":""},{"name":"Linux build (FIXTURE)","bucket":"pending","state":"IN_PROGRESS","link":""}]`, nil
		}
	case "ssh":
		// Rex hosts later GUI panes with its own environment. Ignore discovery
		// options such as an inherited EXE_HOST/key; this binary never invokes SSH.
		if len(args) >= 3 && args[len(args)-2] == "ls" && args[len(args)-1] == "--json" {
			return `{"vms":[{"vm_name":"demo-api","status":"running","ssh_dest":"demo-api.invalid","https_url":"https://demo-api.invalid"},{"vm_name":"demo-worker","status":"running","ssh_dest":"demo-worker.invalid","https_url":"https://demo-worker.invalid"},{"vm_name":"demo-sandbox","status":"stopped","ssh_dest":"demo-sandbox.invalid","https_url":"https://demo-sandbox.invalid"}]}`, nil
		}
	}
	return "", fmt.Errorf("FIXTURE: unsupported %s action; no network, browser, checkout or SSH action was performed", name)
}

func allowedGit(c providerConfig, args []string, cwd string) error {
	anchor := cwd
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--literal-pathspecs", "--no-optional-locks", "--no-pager":
			i++
		case "-C":
			if i+1 >= len(args) {
				return errors.New("fixture git missing root")
			}
			anchor = args[i+1]
			i += 2
		default:
			goto command
		}
	}
command:
	if !samePath(anchor, c.Root) || i >= len(args) {
		return errors.New("fixture git requires the disposable workspace root")
	}
	tail := args[i:]
	valid := false
	switch tail[0] {
	case "rev-parse":
		valid = len(tail) == 2 && tail[1] == "--show-toplevel"
	case "symbolic-ref":
		valid = len(tail) == 4 && strings.Join(tail, " ") == "symbolic-ref --quiet --short HEAD"
	case "config":
		valid = len(tail) == 3 && tail[1] == "--get-regexp" && tail[2] == `^remote\..*\.url$`
	case "status":
		valid = len(tail) >= 2 && tail[1] == "--porcelain=v1"
	case "diff":
		valid = len(tail) >= 4 && tail[1] == "--no-ext-diff" && tail[2] == "--no-textconv" && tail[3] == "--color=never"
	}
	if !valid {
		return errors.New("FIXTURE: unsupported Git command; no action performed")
	}
	return nil
}
func readGit(ctx context.Context, c providerConfig, args []string, out, errOut io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := allowedGit(c, args, cwd); err != nil {
		return err
	}
	argv := append([]string{"-c", "core.hooksPath=" + filepath.Join(c.TempDir, "empty"), "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}, args...)
	cmd := exec.CommandContext(ctx, c.Git, argv...)
	cmd.Env = gitEnv(c)
	cmd.Dir = c.Root
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}
