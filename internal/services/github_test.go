package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/metcalfc/rawr/internal/model"
)

const openPRJSON = `[{"number":42,"title":"A useful change","url":"https://github.com/owner/repo/pull/42","state":"OPEN"}]`
const openIssueJSON = `[{"number":9,"title":"Track a problem","url":"https://github.com/owner/repo/issues/9","state":"OPEN"}]`

func selectedPR() model.PR {
	return model.PR{Number: 42, Title: "A useful change", URL: "https://github.com/owner/repo/pull/42", State: "OPEN"}
}

func TestGitHubRepositoryWideQueriesAndLazySelectedChecks(t *testing.T) {
	root := t.TempDir()
	calls := fixture(t, result{stdout: "feature/work\n"}, result{stdout: openPRJSON}, result{stdout: openIssueJSON}, result{code: 8, stdout: `[{"name":"build","bucket":"pending","state":"QUEUED"}]`})
	status := GitHub(context.Background(), root, "owner/repo")
	if status.State != "ready" || status.Branch != "feature/work" || len(status.PRs) != 1 || len(status.Issues) != 1 || status.PRs[0].ChecksLoaded || status.PRs[0].Checks != nil || len(*calls) != 3 {
		t.Fatalf("unexpected repository view: %+v calls=%v", status, *calls)
	}
	expected := []string{"gh", "pr", "list", "--repo", "owner/repo", "--state", "open", "--limit", "100", "--json", "number,title,url,state"}
	if !reflect.DeepEqual((*calls)[1].argv, expected) || (*calls)[1].cwd != root || (*calls)[2].cwd != root {
		t.Fatalf("unanchored/restricted query: %+v", *calls)
	}
	if strings.Contains(strings.Join((*calls)[1].argv, " "), "--head") {
		t.Fatal("PRs were restricted to the local branch")
	}
	checks, err := GitHubChecks(context.Background(), root, "owner/repo", status.PRs[0])
	if err != nil || len(checks) != 1 || checks[0].Bucket != "pending" || len(*calls) != 4 {
		t.Fatalf("pending selected checks: %+v %v", checks, err)
	}
}

func TestGitHubRemoteResolutionAndAmbiguity(t *testing.T) {
	for _, test := range []struct{ name, remotes, want string }{
		{"same repository", "remote.origin.url git@github.com:Owner/Repo.git\nremote.mirror.url https://user:secret@github.com/owner/repo.git\n", "ready"},
		{"ambiguous", "remote.origin.url git@github.com:owner/repo.git\nremote.upstream.url https://github.com/other/project.git\n", "Multiple GitHub repositories"},
		{"missing", "remote.origin.url https://user:secret@example.com/repo.git\n", "No GitHub repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture(t, result{stdout: test.remotes}, result{stdout: "main"}, result{stdout: "[]"}, result{stdout: "[]"})
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

func TestDetachedHEADAndUnavailableBranchStillBrowseRepository(t *testing.T) {
	for _, branch := range []result{{code: 1}, {code: 128, stderr: "not a git checkout"}} {
		fixture(t, branch, result{stdout: openPRJSON}, result{stdout: openIssueJSON})
		status := GitHub(context.Background(), t.TempDir(), "owner/repo")
		if status.State != "ready" || len(status.PRs) != 1 || len(status.Issues) != 1 {
			t.Fatalf("branch state blocked repository browsing: %+v", status)
		}
		if branch.code == 1 && !status.Detached {
			t.Fatal("detached branch indication lost")
		}
	}
}

func TestGitHubCategoryFailuresRemainActionableAndPartial(t *testing.T) {
	for _, test := range []struct {
		name     string
		response result
		want     string
	}{
		{"auth", result{code: 4, stderr: "bad credentials"}, "gh auth login"},
		{"offline", result{code: 1, stderr: "could not resolve github.com"}, "unreachable"},
		{"malformed", result{stdout: "bad json"}, "malformed JSON"},
		{"object", result{stdout: "{}"}, "malformed JSON"},
		{"null", result{stdout: "null"}, "expected an array"},
		{"foreign URL", result{stdout: `[{"number":3,"url":"https://github.com/other/repo/pull/3","state":"OPEN"}]`}, "outside the declared repository"},
		{"bad number", result{stdout: `[{"number":true,"url":"https://github.com/owner/repo/pull/3","state":"OPEN"}]`}, "invalid PR identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture(t, result{stdout: "main"}, test.response, result{stdout: openIssueJSON})
			status := GitHub(context.Background(), t.TempDir(), "owner/repo")
			if status.State != "partial" || !status.Partial || !strings.Contains(status.PRsError, test.want) || len(status.Issues) != 1 {
				t.Fatalf("category error discarded valid issues: %+v", status)
			}
		})
	}
	fixture(t, result{stdout: "main"}, result{code: 4, stderr: "not logged in"}, result{code: 4, stderr: "not logged in"})
	if status := GitHub(context.Background(), t.TempDir(), "owner/repo"); status.State != "error" || status.PRsError == "" || status.IssuesError == "" {
		t.Fatalf("all-provider failure misreported: %+v", status)
	}
}

func TestIssueURLIdentityAndResponseBounds(t *testing.T) {
	for _, raw := range []string{`[{"number":1,"state":"OPEN","url":"https://example.com/issues/1"}]`, `[{"number":0,"state":"OPEN"}]`, `[null]`} {
		fixture(t, result{stdout: "main"}, result{stdout: openPRJSON}, result{stdout: raw})
		status := GitHub(context.Background(), t.TempDir(), "owner/repo")
		if status.State != "partial" || status.IssuesError == "" || len(status.PRs) != 1 {
			t.Fatalf("invalid issues accepted: %+v", status)
		}
	}
	items := make([]model.PR, 101)
	for i := range items {
		items[i] = selectedPR()
	}
	raw, _ := json.Marshal(items)
	fixture(t, result{stdout: "main"}, result{stdout: string(raw)}, result{stdout: "[]"})
	if status := GitHub(context.Background(), t.TempDir(), "owner/repo"); !strings.Contains(status.PRsError, "100 item") {
		t.Fatalf("unbounded response accepted: %+v", status)
	}
}

func TestSelectedChecksErrorsNeverAlterPRAndValidFailureExitIsData(t *testing.T) {
	for _, code := range []int{0, 1, 8} {
		fixture(t, result{code: code, stdout: `[{"name":"build","bucket":"fail","state":"FAILURE"}]`})
		checks, err := GitHubChecks(context.Background(), t.TempDir(), "owner/repo", selectedPR())
		if err != nil || len(checks) != 1 {
			t.Fatalf("valid checks exit %d rejected: %+v %v", code, checks, err)
		}
	}
	for _, response := range []result{{code: 4, stderr: "authentication failed"}, {stdout: "bad-json"}, {stdout: `[null]`}} {
		fixture(t, response)
		pr := selectedPR()
		before := pr
		if _, err := GitHubChecks(context.Background(), t.TempDir(), "owner/repo", pr); err == nil || !reflect.DeepEqual(pr, before) {
			t.Fatalf("invalid checks accepted or PR changed: %v", err)
		}
	}
}

type checkoutHarness struct {
	root                       string
	paths                      []string
	calls                      []invocation
	dirty                      string
	dirtySecond                string
	stateFile                  string
	remote                     string
	fresh                      model.PR
	checkoutErr                error
	checkoutCode               int
	statuses, views, checkouts int
}

func checkoutFixture(t *testing.T) *checkoutHarness {
	t.Helper()
	h := &checkoutHarness{root: t.TempDir(), remote: "remote.origin.url git@github.com:owner/repo.git\n", fresh: selectedPR()}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "BISECT_START", "rebase-merge", "rebase-apply", "sequencer"} {
		h.paths = append(h.paths, filepath.Join(h.root, name))
	}
	previous := run
	t.Cleanup(func() { run = previous })
	run = func(ctx context.Context, argv []string, cwd string, timeout time.Duration) (result, error) {
		h.calls = append(h.calls, invocation{argv, cwd, timeout})
		joined := strings.Join(argv, " ")
		switch {
		case strings.Contains(joined, "config --get-regexp"):
			return result{stdout: h.remote}, nil
		case strings.Contains(joined, "rev-parse --show-toplevel"):
			return result{stdout: h.root}, nil
		case strings.Contains(joined, "status --porcelain=v1"):
			h.statuses++
			dirty := h.dirty
			if h.statuses > 1 && h.dirtySecond != "" {
				dirty = h.dirtySecond
			}
			return result{stdout: dirty}, nil
		case strings.Contains(joined, "rev-parse --path-format=absolute"):
			return result{stdout: strings.Join(h.paths, "\n")}, nil
		case len(argv) > 2 && argv[0] == "gh" && argv[2] == "view":
			h.views++
			raw, _ := json.Marshal(h.fresh)
			return result{stdout: string(raw)}, nil
		case len(argv) > 2 && argv[0] == "gh" && argv[2] == "checkout":
			h.checkouts++
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 60*time.Second {
				t.Fatal("checkout had no bounded overall deadline")
			}
			return result{code: h.checkoutCode}, h.checkoutErr
		default:
			t.Fatalf("unexpected checkout subprocess: %q", argv)
			return result{}, errors.New("unexpected subprocess")
		}
	}
	return h
}

func TestCheckoutPRUsesLiteralBoundedSubprocessAndRechecksCleanTree(t *testing.T) {
	h := checkoutFixture(t)
	if err := CheckoutPR(context.Background(), h.root, "owner/repo", selectedPR()); err != nil {
		t.Fatal(err)
	}
	if h.checkouts != 1 || h.statuses != 2 || h.views != 1 {
		t.Fatalf("checkout preconditions missing: %+v", h)
	}
	call := h.calls[len(h.calls)-1]
	if !reflect.DeepEqual(call.argv, []string{"gh", "pr", "checkout", "42", "--repo", "owner/repo"}) || call.cwd != h.root || call.timeout != 45*time.Second {
		t.Fatalf("checkout command escaped root or forced branch: %+v", call)
	}
}

func TestCheckoutRefusesDirtyTrackedUntrackedAndGitOperations(t *testing.T) {
	for _, dirty := range []string{" M tracked.txt\x00", "?? untracked.txt\x00"} {
		t.Run(dirty, func(t *testing.T) {
			h := checkoutFixture(t)
			h.dirty = dirty
			if err := CheckoutPR(context.Background(), h.root, "owner/repo", selectedPR()); err == nil || h.checkouts != 0 {
				t.Fatalf("dirty checkout accepted: %v", err)
			}
		})
	}
	for index := 0; index < 8; index++ {
		t.Run(filepath.Base([]string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "BISECT_START", "rebase-merge", "rebase-apply", "sequencer"}[index]), func(t *testing.T) {
			h := checkoutFixture(t)
			if err := os.WriteFile(h.paths[index], []byte("active"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := CheckoutPR(context.Background(), h.root, "owner/repo", selectedPR()); err == nil || h.checkouts != 0 {
				t.Fatalf("in-progress Git operation ignored: %v", err)
			}
		})
	}
	h := checkoutFixture(t)
	h.dirtySecond = "?? arrived-during-network.txt\x00"
	if err := CheckoutPR(context.Background(), h.root, "owner/repo", selectedPR()); err == nil || h.checkouts != 0 {
		t.Fatalf("dirty recheck missed concurrent changes: %v", err)
	}
}

func TestCheckoutRefusesUnverifiedRepositoryAndPRIdentity(t *testing.T) {
	for _, kind := range []string{"different repo", "ambiguous remotes", "closed input", "foreign URL", "closed fresh PR", "wrong fresh identity"} {
		t.Run(kind, func(t *testing.T) {
			h := checkoutFixture(t)
			repo := "owner/repo"
			pr := selectedPR()
			switch kind {
			case "different repo":
				repo = "other/project"
			case "ambiguous remotes":
				h.remote += "remote.upstream.url https://github.com/other/project.git\n"
			case "closed input":
				pr.State = "CLOSED"
			case "foreign URL":
				pr.URL = "https://github.com/other/project/pull/42"
			case "closed fresh PR":
				h.fresh.State = "CLOSED"
			case "wrong fresh identity":
				h.fresh.Number = 99
			}
			if err := CheckoutPR(context.Background(), h.root, repo, pr); err == nil || h.checkouts != 0 {
				t.Fatalf("unverified %s accepted: %v", kind, err)
			}
		})
	}
}

func TestCheckoutFailureIsNotRetried(t *testing.T) {
	h := checkoutFixture(t)
	h.checkoutErr = context.DeadlineExceeded
	if err := CheckoutPR(context.Background(), h.root, "owner/repo", selectedPR()); err == nil || !strings.Contains(err.Error(), "inspect Git HEAD/status") || h.checkouts != 1 {
		t.Fatalf("ambiguous checkout failure retried/misreported: %v", err)
	}
}
