package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/metcalfc/rawr/internal/model"
)

type invocation struct {
	argv    []string
	cwd     string
	timeout time.Duration
}

func fixture(t *testing.T, outputs ...result) *[]invocation {
	t.Helper()
	previous := run
	t.Cleanup(func() { run = previous })
	calls := []invocation{}
	run = func(_ context.Context, argv []string, cwd string, timeout time.Duration) (result, error) {
		calls = append(calls, invocation{argv, cwd, timeout})
		if len(outputs) == 0 {
			t.Errorf("unexpected subprocess: %q", argv)
			return result{}, errors.New("unexpected subprocess")
		}
		out := outputs[0]
		outputs = outputs[1:]
		return out, nil
	}
	return &calls
}

func TestGitHubAnchorsQueriesAndAcceptsPendingChecks(t *testing.T) {
	root := t.TempDir()
	calls := fixture(t,
		result{stdout: "feature/work\n"},
		result{stdout: `[{"number":42,"title":"A useful change","url":"https://github.com/owner/repo/pull/42","state":"OPEN"}]`},
		result{code: 8, stdout: `[{"name":"build","bucket":"pending","state":"QUEUED","link":"https://github.com/owner/repo/actions/runs/12"}]`},
	)
	status := GitHub(context.Background(), root, "owner/repo")
	if status.State != "ready" || status.Branch != "feature/work" || len(status.PRs) != 1 || len(status.PRs[0].Checks) != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.PRs[0].Checks[0].Bucket != "pending" {
		t.Fatalf("lost check data: %+v", status.PRs[0])
	}
	expected := []string{"gh", "pr", "list", "--repo", "owner/repo", "--head", "feature/work", "--state", "all", "--limit", "10", "--json", "number,title,url,state"}
	if !reflect.DeepEqual((*calls)[1].argv, expected) || (*calls)[1].cwd != root || (*calls)[2].cwd != root {
		t.Fatalf("queries escaped their project: %+v", *calls)
	}
}

func TestGitHubRemoteResolutionAndAmbiguity(t *testing.T) {
	for _, test := range []struct{ name, remotes, want string }{
		{"same repository", "remote.origin.url git@github.com:Owner/Repo.git\nremote.mirror.url https://user:secret@github.com/owner/repo.git\n", "no-pr"},
		{"ambiguous", "remote.origin.url git@github.com:owner/repo.git\nremote.upstream.url https://github.com/other/project.git\n", "Multiple GitHub repositories"},
		{"missing", "remote.origin.url https://user:secret@example.com/repo.git\n", "No GitHub repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture(t, result{stdout: test.remotes}, result{stdout: "main"}, result{stdout: "[]"})
			status := GitHub(context.Background(), t.TempDir(), "")
			if status.State != test.want && !strings.Contains(status.Message, test.want) {
				t.Fatalf("got %+v", status)
			}
			if strings.Contains(status.Message, "secret") {
				t.Fatal("remote credentials leaked")
			}
		})
	}
}

func TestGitHubActionableFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		outputs []result
		want    string
		state   string
	}{
		{"detached", []result{{code: 1}}, "Detached HEAD", "detached"},
		{"auth", []result{{stdout: "main"}, {code: 4, stderr: "bad credentials"}}, "gh auth login", "error"},
		{"offline", []result{{stdout: "main"}, {code: 1, stderr: "could not resolve github.com"}}, "unreachable", "error"},
		{"malformed", []result{{stdout: "main"}, {stdout: "bad json"}}, "malformed JSON", "error"},
		{"object", []result{{stdout: "main"}, {stdout: "{}"}}, "malformed JSON", "error"},
		{"null", []result{{stdout: "main"}, {stdout: "null"}}, "expected an array", "error"},
		{"foreign URL", []result{{stdout: "main"}, {stdout: `[{"number":3,"url":"https://github.com/other/repo/pull/3"}]`}}, "outside the declared repository", "error"},
		{"bad number", []result{{stdout: "main"}, {stdout: `[{"number":true,"url":"https://github.com/owner/repo/pull/3"}]`}}, "invalid PR identity", "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture(t, test.outputs...)
			status := GitHub(context.Background(), t.TempDir(), "owner/repo")
			if status.State != test.state || !strings.Contains(status.Message, test.want) {
				t.Fatalf("unexpected failure: %+v", status)
			}
		})
	}
}

func TestGitHubPreservesPRWhenChecksFail(t *testing.T) {
	for _, checks := range []result{{code: 4, stderr: "authentication failed"}, {stdout: "bad-json"}, {stdout: `[null]`}} {
		fixture(t, result{stdout: "main"}, result{stdout: `[{"number":2,"url":"https://github.com/owner/repo/pull/2","state":"OPEN"}]`}, checks)
		status := GitHub(context.Background(), t.TempDir(), "owner/repo")
		if status.State != "ready" || len(status.PRs) != 1 || status.PRs[0].ChecksError == "" {
			t.Fatalf("lost PR: %+v", status)
		}
	}
}

func TestRepositoryValidationDoesNotRunTools(t *testing.T) {
	calls := fixture(t)
	for _, repo := range []string{"owner/repo;rm -rf /", "../repo", "owner/..", "owner/repo/extra"} {
		status := GitHub(context.Background(), t.TempDir(), repo)
		if status.State != "error" || !strings.Contains(status.Message, "OWNER/NAME") {
			t.Fatalf("accepted %q: %+v", repo, status)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("invalid repository reached a subprocess")
	}
}

func TestVMDiscoveryUsesSSHEnvironmentWithoutShell(t *testing.T) {
	t.Setenv("EXE_HOST", "user@custom.example")
	t.Setenv("EXE_SSH_KEY", "~/keys/my key")
	calls := fixture(t, result{stdout: `{"vms":[{"vm_name":"my-vm","status":"running","ssh_dest":"user@my-vm.exe.xyz"}]}`})
	vms, err := VMs(context.Background())
	if err != nil || len(vms) != 1 {
		t.Fatalf("VMs: %+v %v", vms, err)
	}
	home, _ := os.UserHomeDir()
	expected := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", "-i", filepath.Join(home, "keys/my key"), "-o", "IdentitiesOnly=yes", "user@custom.example", "ls", "--json"}
	if !reflect.DeepEqual((*calls)[0].argv, expected) || (*calls)[0].timeout != 25*time.Second {
		t.Fatalf("unexpected discovery command: %+v", *calls)
	}
	argv, err := SSHArgs(vms[0])
	if err != nil || argv[len(argv)-1] != "user@my-vm.exe.xyz" || argv[2] != "StrictHostKeyChecking=accept-new" {
		t.Fatalf("unexpected shell: %q %v", argv, err)
	}
}

func TestVMDiscoveryRejectsInvalidProviderData(t *testing.T) {
	for _, raw := range []string{
		`[]`, `null`, `{}`, `{"vms":null}`, `{"error":"denied","vms":[]}`, `{"vms":[{"vm_name":"-bad"}]}`,
		`{"vms":[{"vm_name":"vm;whoami"}]}`, `{"vms":[{"vm_name":"vm"},{"vm_name":"vm"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			fixture(t, result{stdout: raw})
			if _, err := VMs(context.Background()); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
}

func TestSSHAndURLRejectOptionsControlsCredentials(t *testing.T) {
	t.Setenv("EXE_HOST", "-ProxyCommand=oops")
	calls := fixture(t)
	if _, err := VMs(context.Background()); err == nil || len(*calls) != 0 {
		t.Fatal("unsafe host reached subprocess")
	}
	for _, dest := range []string{"-oProxyCommand=oops", "host;touch /tmp/pwn", "host\nwhoami", "host with space", "$(whoami)"} {
		if _, err := SSHArgs(model.VM{Name: "safe", SSHDest: dest}); err == nil {
			t.Fatalf("accepted destination %q", dest)
		}
	}
	for _, u := range []string{"javascript:alert(1)", "https://user:password@example.com", "https://example.com\nboom", "file:///tmp/a", "https:///missing"} {
		if _, err := BrowserURL(model.VM{Name: "safe", HTTPSURL: u}); err == nil {
			t.Fatalf("accepted URL %q", u)
		}
		if err := OpenURL(context.Background(), u); err == nil {
			t.Fatalf("opened URL %q", u)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("invalid URL reached subprocess")
	}
}

func TestFindVMMissingDoesNotInventOrMutate(t *testing.T) {
	fixture(t, result{stdout: `{"vms":[{"vm_name":"other"}]}`})
	if _, err := FindVM(context.Background(), "linked"); err == nil || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenersPassLiteralArguments(t *testing.T) {
	file := filepath.Join(t.TempDir(), "$(whoami); file.txt")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := fixture(t, result{}, result{})
	url := "https://example.com/path?q=$(whoami);literal"
	if err := OpenURL(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if err := OpenFile(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if last := (*calls)[0].argv; last[len(last)-1] != url {
		t.Fatalf("URL argv changed: %q", last)
	}
	if last := (*calls)[1].argv; last[len(last)-1] != file {
		t.Fatalf("file argv changed: %q", last)
	}
}

func TestDetailsInertAndBounded(t *testing.T) {
	text := detail("\x1b[31mfailed\x1b[0m\n\t" + strings.Repeat("x", 500))
	if strings.ContainsAny(text, "\x1b\n\t") || len([]rune(text)) > 350 {
		t.Fatalf("unsafe detail %q", text)
	}
}

func TestExecuteCancellationAndMissingTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := execute(ctx, []string{"this-tool-does-not-exist-rawr"}, "", time.Second); err == nil {
		t.Fatal("expected canceled request error")
	}
	if _, err := execute(context.Background(), []string{"this-tool-does-not-exist-rawr"}, "", time.Second); err == nil || !strings.Contains(err.Error(), "missing executable") {
		t.Fatalf("missing tool error: %v", err)
	}
}
