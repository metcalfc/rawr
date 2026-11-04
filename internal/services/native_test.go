package services

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Explicitly opt in: this performs read-only requests with the existing gh/SSH auth.
// The evidence stores counts and field coverage, never credentials or VM addresses.
func TestNativeServices(t *testing.T) {
	path := os.Getenv("RAWR_NATIVE_EVIDENCE")
	if path == "" {
		t.Skip("set RAWR_NATIVE_EVIDENCE to record a real read-only service smoke")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	repository := "metcalfc/rawr"
	projectContext := "existing workspace (no synthetic Git metadata)"
	if fixtureRepository := os.Getenv("RAWR_NATIVE_FIXTURE_REPOSITORY"); fixtureRepository != "" {
		repository = fixtureRepository
		root = t.TempDir()
		if out, err := exec.CommandContext(ctx, "git", "init", "--quiet", "--initial-branch=main", root).CombinedOutput(); err != nil {
			t.Fatalf("temporary Git context: %v: %s", err, out)
		}
		projectContext = "temporary empty Git checkout on main; explicit repository; no repository content cloned"
	}
	github := GitHub(ctx, root, repository)
	vms, vmErr := VMs(ctx)
	fields := map[string]int{}
	for _, vm := range vms {
		if vm.Name != "" {
			fields["vm_name"]++
		}
		if vm.Status != "" {
			fields["status"]++
		}
		if vm.SSHDest != "" {
			fields["ssh_dest"]++
		}
		if vm.HTTPSURL != "" {
			fields["https_url"]++
		}
		if vm.TerminalURL != "" {
			fields["terminal_url"]++
		}
		if vm.ShelleyURL != "" {
			fields["shelley_url"]++
		}
	}
	checks := 0
	checksError := ""
	checksRequested := len(github.PRs) > 0
	if checksRequested {
		selected, err := GitHubChecks(ctx, root, repository, github.PRs[0])
		checks = len(selected)
		if err != nil {
			checksError = err.Error()
		}
	}
	vmError := ""
	if vmErr != nil {
		vmError = vmErr.Error()
	}
	evidence := map[string]any{
		"recorded_at": time.Now().UTC().Format(time.RFC3339), "go_version": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
		"github":          map[string]any{"repository": github.Repository, "branch": github.Branch, "state": github.State, "message": github.Message, "pr_count": len(github.PRs), "issue_count": len(github.Issues), "check_count": checks, "selected_checks_requested": checksRequested, "checks_error": checksError, "partial": github.Partial, "commands": []string{"git symbolic-ref --quiet --short HEAD", "gh pr list --repo " + repository + " --state open --limit 100 --json number,title,url,state", "gh issue list --repo " + repository + " --state open --limit 100 --json number,title,url,state", "gh pr checks <number> --repo " + repository + " --json name,bucket,state,link (selected open PR only; lazy)"}},
		"exe":             map[string]any{"vm_count": len(vms), "field_coverage": fields, "error": vmError, "command": "ssh -o BatchMode=yes -o ConnectTimeout=15 [EXE_SSH_KEY options] [EXE_HOST or exe.dev] ls --json", "authentication": "existing SSH agent/key only; no credential copying"},
		"scope":           "read-only discovery; no VM lifecycle, browser launch, or interactive shell performed",
		"project_context": projectContext,
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if github.State == "error" || github.Partial {
		t.Errorf("GitHub discovery: %s", github.Message)
	}
	if checksError != "" {
		t.Errorf("Selected PR checks: %s", checksError)
	}
	if vmErr != nil {
		t.Errorf("exe discovery: %v", vmErr)
	}
}
