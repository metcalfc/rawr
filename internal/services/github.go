package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/metcalfc/rawr/internal/model"
)

const githubLimit = 100

func validatePR(pr model.PR, repo string) error {
	if pr.Number <= 0 || pr.State != "OPEN" {
		return errors.New("select a known open pull request")
	}
	if !strings.EqualFold(pr.URL, fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr.Number)) {
		return errors.New("pull request URL is outside the declared repository")
	}
	return nil
}

func githubPRs(ctx context.Context, root, repo string) ([]model.PR, error) {
	r, err := run(ctx, []string{"gh", "pr", "list", "--repo", repo, "--state", "open", "--limit", strconv.Itoa(githubLimit), "--json", "number,title,url,state"}, root, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, errors.New(ghError(r))
	}
	items, err := parseArray(r.stdout, "gh pr list")
	if err != nil {
		return nil, err
	}
	if len(items) > githubLimit {
		return nil, errors.New("gh pr list exceeded the 100 item response limit")
	}
	prs := make([]model.PR, 0, len(items))
	seen := map[int]bool{}
	for _, raw := range items {
		var pr model.PR
		if err := json.Unmarshal(raw, &pr); err != nil || pr.Number <= 0 {
			return nil, errors.New("gh pr list returned an invalid PR identity")
		}
		if err := validatePR(pr, repo); err != nil {
			return nil, fmt.Errorf("gh pr list: %w", err)
		}
		if seen[pr.Number] {
			return nil, errors.New("gh pr list returned duplicate PR identities")
		}
		seen[pr.Number] = true
		pr.Checks = nil
		pr.ChecksError = ""
		pr.ChecksLoaded = false
		prs = append(prs, pr)
	}
	return prs, nil
}

func githubIssues(ctx context.Context, root, repo string) ([]model.Issue, error) {
	r, err := run(ctx, []string{"gh", "issue", "list", "--repo", repo, "--state", "open", "--limit", strconv.Itoa(githubLimit), "--json", "number,title,url,state"}, root, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, errors.New(ghError(r))
	}
	items, err := parseArray(r.stdout, "gh issue list")
	if err != nil {
		return nil, err
	}
	if len(items) > githubLimit {
		return nil, errors.New("gh issue list exceeded the 100 item response limit")
	}
	issues := make([]model.Issue, 0, len(items))
	seen := map[int]bool{}
	for _, raw := range items {
		var issue model.Issue
		if err := json.Unmarshal(raw, &issue); err != nil || issue.Number <= 0 || issue.State != "OPEN" {
			return nil, errors.New("gh issue list returned an invalid open issue")
		}
		if !strings.EqualFold(issue.URL, fmt.Sprintf("https://github.com/%s/issues/%d", repo, issue.Number)) {
			return nil, errors.New("gh issue list returned an issue URL outside the declared repository")
		}
		if seen[issue.Number] {
			return nil, errors.New("gh issue list returned duplicate issue identities")
		}
		seen[issue.Number] = true
		issues = append(issues, issue)
	}
	return issues, nil
}

// GitHub browses the entire declared repository, independent of the local
// branch. Lists are bounded and checks are loaded separately for the selected PR.
// Category errors remain distinct so the UI can retain old data on a failed refresh.
func GitHub(ctx context.Context, root, repository string) model.GitHubStatus {
	status := model.GitHubStatus{State: "error", PRs: []model.PR{}, Issues: []model.Issue{}}
	repo, err := resolveRepository(ctx, root, repository)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	status.Repository = repo
	notices := []string{}
	branch, err := run(ctx, []string{"git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD"}, "", 20*time.Second)
	if err == nil && branch.code == 0 && strings.TrimSpace(branch.stdout) != "" {
		status.Branch = strings.TrimSpace(branch.stdout)
	} else if err == nil && branch.code == 1 {
		status.Detached = true
		notices = append(notices, "Detached HEAD; repository browsing remains available.")
	} else {
		notices = append(notices, "Local branch unavailable; browsing the declared repository.")
	}
	prs, prErr := githubPRs(ctx, root, repo)
	issues, issueErr := githubIssues(ctx, root, repo)
	status.PRs, status.Issues = prs, issues
	status.State = "ready"
	if prErr != nil {
		status.PRsError = prErr.Error()
		notices = append(notices, "PRs: "+prErr.Error())
	}
	if issueErr != nil {
		status.IssuesError = issueErr.Error()
		notices = append(notices, "Issues: "+issueErr.Error())
	}
	if prErr != nil || issueErr != nil {
		status.Partial = true
		status.State = "partial"
	}
	if prErr != nil && issueErr != nil {
		status.State = "error"
	}
	if len(prs) == githubLimit || len(issues) == githubLimit {
		notices = append(notices, "Showing at most 100 open PRs and 100 open issues.")
	}
	if len(notices) == 0 && len(prs) == 0 && len(issues) == 0 {
		notices = append(notices, "No open pull requests or issues in this repository.")
	}
	status.Message = strings.Join(notices, " ")
	status.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return status
}

// GitHubChecks fetches only the explicitly selected open PR. Pending and failing
// exit codes still carry valid check data; an error never mutates the caller's PR.
func GitHubChecks(ctx context.Context, root, repository string, pr model.PR) ([]model.Check, error) {
	repo, err := resolveRepository(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	if err := validatePR(pr, repo); err != nil {
		return nil, err
	}
	r, err := run(ctx, []string{"gh", "pr", "checks", strconv.Itoa(pr.Number), "--repo", repo, "--json", "name,bucket,state,link"}, root, 20*time.Second)
	if err != nil {
		return nil, err
	}
	if r.code != 0 && r.code != 1 && r.code != 8 {
		return nil, errors.New(ghError(r))
	}
	rows, err := parseArray(r.stdout, "gh pr checks")
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, errors.New("gh pr checks exceeded the 1000 check response limit")
	}
	checks := make([]model.Check, 0, len(rows))
	for _, row := range rows {
		var check model.Check
		if string(row) == "null" || json.Unmarshal(row, &check) != nil {
			return nil, errors.New("gh pr checks returned an invalid check")
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func cleanCheckoutWorktree(ctx context.Context, root string) error {
	r, err := run(ctx, []string{"git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none"}, "", 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New("cannot inspect the local Git worktree; checkout refused")
	}
	if r.stdout != "" {
		return errors.New("checkout requires a clean worktree, including untracked files; commit or move your changes first")
	}
	states := []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "BISECT_START", "rebase-merge", "rebase-apply", "sequencer"}
	argv := []string{"git", "-C", root, "rev-parse", "--path-format=absolute"}
	for _, state := range states {
		argv = append(argv, "--git-path", state)
	}
	r, err = run(ctx, argv, "", 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New("cannot inspect Git operation state; checkout refused")
	}
	paths := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(paths) != len(states) {
		return errors.New("Git returned incomplete operation-state paths; checkout refused")
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return errors.New("Git returned a nonabsolute operation-state path; checkout refused")
		}
		if _, err := os.Lstat(path); err == nil {
			return errors.New("a merge, rebase, cherry-pick, revert or bisect is in progress; checkout refused")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot inspect Git operation state: %w", err)
		}
	}
	return nil
}

// CheckoutPR changes the verified local worktree through a separate gh process.
// The host must additionally verify its anchored center is local and idle. This
// service never writes to a terminal, stashes changes, resets or forces a branch.
func CheckoutPR(ctx context.Context, root, repository string, pr model.PR) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	local, err := resolveRepository(ctx, root, "")
	if err != nil {
		return err
	}
	if repository == "" {
		repository = local
	}
	repo, err := repositoryName(repository)
	if err != nil {
		return err
	}
	if !strings.EqualFold(repo, local) {
		return errors.New("declared repository differs from the local Git remotes; checkout refused")
	}
	if err := validatePR(pr, repo); err != nil {
		return err
	}
	r, err := run(ctx, []string{"git", "-C", root, "rev-parse", "--show-toplevel"}, "", 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New("checkout requires an existing local Git worktree")
	}
	actual, err := filepath.EvalSymlinks(strings.TrimSpace(r.stdout))
	if err != nil {
		return err
	}
	wanted, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	actual, _ = filepath.Abs(actual)
	wanted, _ = filepath.Abs(wanted)
	if actual != wanted {
		return errors.New("launch ghrawr at the Git worktree root before checkout")
	}
	if err := cleanCheckoutWorktree(ctx, root); err != nil {
		return err
	}
	r, err = run(ctx, []string{"gh", "pr", "view", strconv.Itoa(pr.Number), "--repo", repo, "--json", "number,url,state"}, root, 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New(ghError(r))
	}
	var fresh model.PR
	if err := json.Unmarshal([]byte(r.stdout), &fresh); err != nil || fresh.Number != pr.Number {
		return errors.New("GitHub returned an invalid PR identity; checkout refused")
	}
	if err := validatePR(fresh, repo); err != nil {
		return fmt.Errorf("PR is no longer verified open; refresh before checkout: %w", err)
	}
	current, err := resolveRepository(ctx, root, "")
	if err != nil {
		return err
	}
	if !strings.EqualFold(current, repo) {
		return errors.New("local repository changed during checkout verification; refused")
	}
	if err := cleanCheckoutWorktree(ctx, root); err != nil {
		return err
	}
	r, err = run(ctx, []string{"gh", "pr", "checkout", strconv.Itoa(pr.Number), "--repo", repo}, root, 45*time.Second)
	if err != nil {
		return fmt.Errorf("checkout did not complete; inspect Git HEAD/status before retrying: %w", err)
	}
	if r.code != 0 {
		return fmt.Errorf("checkout failed; inspect Git HEAD/status before retrying: %s", ghError(r))
	}
	return nil
}
