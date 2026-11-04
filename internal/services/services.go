// Package services provides bounded, read-only adapters for the standalone apps.
// Every subprocess uses literal argv; no service controls VM lifecycle or copies credentials.
package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/metcalfc/rawr/internal/model"
)

type result struct {
	stdout, stderr string
	code           int
}
type runner func(context.Context, []string, string, time.Duration) (result, error)

// Kept behind one boundary so fixtures can verify exact argv without executing tools.
var run runner = execute

type boundedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (2 << 20) - b.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:max(0, remaining)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func execute(ctx context.Context, argv []string, cwd string, timeout time.Duration) (result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() != nil {
		return r, fmt.Errorf("%s timed out or was canceled; check connectivity and refresh", argv[0])
	}
	if stdout.overflow || stderr.overflow {
		return r, fmt.Errorf("%s returned too much output; narrow the request and refresh", argv[0])
	}
	if err == nil {
		return r, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
		return r, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return r, fmt.Errorf("missing executable: %s; install it and refresh", argv[0])
	}
	return r, fmt.Errorf("could not run %s: %s", argv[0], detail(err.Error()))
}

var ansiCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func detail(s string) string {
	s = ansiCSI.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 350 {
		s = string(r[:350])
	}
	return s
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func repositoryName(s string) (string, error) {
	if repositoryPattern.MatchString(s) {
		p := strings.Split(s, "/")
		if p[0] != "." && p[0] != ".." && p[1] != "." && p[1] != ".." {
			return s, nil
		}
	}
	return "", errors.New("repository must be an explicit GitHub OWNER/NAME")
}

func remoteRepository(s string) string {
	var path string
	if strings.HasPrefix(s, "git@github.com:") {
		path = strings.TrimPrefix(s, "git@github.com:")
	} else {
		u, err := url.Parse(s)
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
			return ""
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return ""
		}
		path = strings.TrimLeft(u.Path, "/")
	}
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	name, _ := repositoryName(path)
	return name
}

func resolveRepository(ctx context.Context, root, repository string) (string, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project root is inaccessible: %s", root)
	}
	if repository != "" {
		return repositoryName(repository)
	}
	r, err := run(ctx, []string{"git", "-C", root, "config", "--get-regexp", `^remote\..*\.url$`}, "", 20*time.Second)
	if err != nil {
		return "", err
	}
	if r.code != 0 && r.code != 1 {
		return "", errors.New("cannot inspect Git remotes; check that the project root is a Git checkout")
	}
	repos := map[string]string{}
	for _, line := range strings.Split(r.stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if found := remoteRepository(fields[1]); found != "" {
			repos[strings.ToLower(found)] = found
		}
	}
	if len(repos) != 1 {
		reason := "No GitHub repository in remotes"
		if len(repos) > 1 {
			reason = "Multiple GitHub repositories in remotes"
		}
		return "", fmt.Errorf("%s; declare OWNER/NAME explicitly for this project", reason)
	}
	for _, repo := range repos {
		return repo, nil
	}
	panic("unreachable")
}

func ghError(r result) string {
	s := r.stderr
	if s == "" {
		s = r.stdout
	}
	s = detail(s)
	lower := strings.ToLower(s)
	if r.code == 4 || containsAny(lower, "authentication", "gh auth login", "bad credentials", "token has expired", "not logged") {
		return "GitHub authentication is unavailable or expired; run gh auth login, then refresh."
	}
	if containsAny(lower, "could not resolve", "error connecting", "network", "connection refused", "timeout", "timed out", "offline") {
		return "GitHub is unreachable; check connectivity and refresh."
	}
	message := fmt.Sprintf("GitHub request failed (exit %d)", r.code)
	if s != "" {
		message += ": " + s
	}
	return message
}
func containsAny(s string, options ...string) bool {
	for _, option := range options {
		if strings.Contains(s, option) {
			return true
		}
	}
	return false
}
func parseArray(raw, label string) ([]json.RawMessage, error) {
	var array []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &array); err != nil {
		return nil, fmt.Errorf("%s returned malformed JSON; refresh or check the tool version", label)
	}
	if strings.TrimSpace(raw) == "null" {
		return nil, fmt.Errorf("%s returned an unexpected response; expected an array", label)
	}
	return array, nil
}

// GitHub remains anchored to root and the declared or unambiguous remote repository.
// Pending/failing checks are data, and a failed checks request preserves its PR.
func GitHub(ctx context.Context, root, repository string) model.GitHubStatus {
	status := model.GitHubStatus{State: "error", PRs: []model.PR{}}
	fail := func(err error) model.GitHubStatus { status.Message = err.Error(); return status }
	repo, err := resolveRepository(ctx, root, repository)
	if err != nil {
		return fail(err)
	}
	status.Repository = repo
	r, err := run(ctx, []string{"git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD"}, "", 20*time.Second)
	if err != nil {
		return fail(err)
	}
	if r.code == 1 {
		status.State = "detached"
		status.Message = "Detached HEAD; select a branch to inspect its pull requests."
		return status
	}
	if r.code != 0 {
		return fail(errors.New("cannot read project branch; check that the project root is a Git checkout"))
	}
	status.Branch = strings.TrimSpace(r.stdout)
	if status.Branch == "" {
		return fail(errors.New("Git returned an empty branch name"))
	}
	r, err = run(ctx, []string{"gh", "pr", "list", "--repo", repo, "--head", status.Branch, "--state", "all", "--limit", "10", "--json", "number,title,url,state"}, root, 20*time.Second)
	if err != nil {
		return fail(err)
	}
	if r.code != 0 {
		return fail(errors.New(ghError(r)))
	}
	items, err := parseArray(r.stdout, "gh pr list")
	if err != nil {
		return fail(err)
	}
	for _, raw := range items {
		var pr model.PR
		if err := json.Unmarshal(raw, &pr); err != nil || pr.Number <= 0 {
			return fail(errors.New("gh pr list returned an invalid PR identity"))
		}
		expected := fmt.Sprintf("https://github.com/%s/pull/%d", repo, pr.Number)
		if !strings.EqualFold(pr.URL, expected) {
			return fail(errors.New("gh pr list returned a PR URL outside the declared repository"))
		}
		pr.Checks = nil // Never trust checks fields from an unexpected gh list response.
		pr.ChecksError = ""
		if pr.State == "OPEN" {
			checks, err := run(ctx, []string{"gh", "pr", "checks", strconv.Itoa(pr.Number), "--repo", repo, "--json", "name,bucket,state,link"}, root, 20*time.Second)
			if err != nil {
				pr.ChecksError = err.Error()
			} else if checks.code != 0 && checks.code != 1 && checks.code != 8 {
				pr.ChecksError = ghError(checks)
			} else {
				rows, parseErr := parseArray(checks.stdout, "gh pr checks")
				if parseErr == nil {
					for _, row := range rows {
						var check model.Check
						if string(row) == "null" || json.Unmarshal(row, &check) != nil {
							parseErr = errors.New("gh pr checks returned an invalid check")
							break
						}
						pr.Checks = append(pr.Checks, check)
					}
				}
				if parseErr != nil {
					pr.Checks = nil
					pr.ChecksError = parseErr.Error()
				}
			}
		}
		status.PRs = append(status.PRs, pr)
	}
	status.State = "ready"
	if len(status.PRs) == 0 {
		status.State = "no-pr"
		status.Message = fmt.Sprintf("No pull request found for %s in %s.", status.Branch, repo)
	}
	return status
}

var destinationPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+@:\[\]-]*$`)
var vmPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func destination(value string) (string, error) {
	if !destinationPattern.MatchString(value) {
		return "", errors.New("exe.dev returned an invalid SSH destination; refresh before opening a shell")
	}
	return value, nil
}
func sshOptions() []string {
	options := []string{"-o", "ConnectTimeout=15"}
	if key := os.Getenv("EXE_SSH_KEY"); key != "" {
		if key == "~" || strings.HasPrefix(key, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				if key == "~" {
					key = home
				} else {
					key = filepath.Join(home, strings.TrimPrefix(key, "~/"))
				}
			}
		}
		options = append(options, "-i", key, "-o", "IdentitiesOnly=yes")
	}
	return options
}

// VMs uses the user's SSH key or agent. It does not create or mutate machines.
func VMs(ctx context.Context) ([]model.VM, error) {
	host := os.Getenv("EXE_HOST")
	if host == "" {
		host = "exe.dev"
	}
	host, err := destination(host)
	if err != nil {
		return nil, err
	}
	argv := append([]string{"ssh", "-o", "BatchMode=yes"}, sshOptions()...)
	argv = append(argv, host, "ls", "--json")
	r, err := run(ctx, argv, "", 25*time.Second)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fmt.Errorf("cannot reach exe.dev; check the SSH key/agent (ssh-add), EXE_HOST and connectivity. %s", detail(r.stderr))
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil || envelope == nil {
		return nil, errors.New("exe.dev ls returned an unexpected response; expected an object with vms")
	}
	if failure, ok := envelope["error"]; ok && string(failure) != "null" && string(failure) != `""` && string(failure) != "false" {
		return nil, fmt.Errorf("exe.dev rejected the request: %s", detail(string(failure)))
	}
	var vms []model.VM
	raw, ok := envelope["vms"]
	if !ok || strings.TrimSpace(string(raw)) == "null" || json.Unmarshal(raw, &vms) != nil {
		return nil, errors.New("exe.dev ls returned an unexpected response; expected a vms array")
	}
	seen := map[string]bool{}
	for _, vm := range vms {
		if !vmPattern.MatchString(vm.Name) {
			return nil, errors.New("exe.dev ls returned an invalid VM identity")
		}
		if seen[vm.Name] {
			return nil, errors.New("exe.dev ls returned duplicate VM identities; refresh before choosing a VM")
		}
		seen[vm.Name] = true
	}
	return vms, nil
}

func FindVM(ctx context.Context, name string) (model.VM, error) {
	if !vmPattern.MatchString(name) {
		return model.VM{}, errors.New("a valid exe.dev VM name is required")
	}
	vms, err := VMs(ctx)
	if err != nil {
		return model.VM{}, err
	}
	for _, vm := range vms {
		if vm.Name == name {
			return vm, nil
		}
	}
	return model.VM{}, fmt.Errorf("linked VM %s was not found; the saved project link is unchanged", name)
}
func SSHArgs(vm model.VM) ([]string, error) {
	if !vmPattern.MatchString(vm.Name) {
		return nil, errors.New("a valid exe.dev VM identity is required")
	}
	dest := vm.SSHDest
	if dest == "" {
		dest = vm.Name + ".exe.xyz"
	}
	dest, err := destination(dest)
	if err != nil {
		return nil, err
	}
	argv := append([]string{"ssh", "-o", "StrictHostKeyChecking=accept-new"}, sshOptions()...)
	return append(argv, dest), nil
}

func validateURL(value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("expected an HTTP(S) URL")
		}
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("expected an HTTP(S) URL without embedded credentials")
	}
	return nil
}
func BrowserURL(vm model.VM) (string, error) {
	if !vmPattern.MatchString(vm.Name) {
		return "", errors.New("a valid exe.dev VM identity is required")
	}
	u := vm.HTTPSURL
	if u == "" {
		u = "https://" + vm.Name + ".exe.xyz"
	}
	return u, validateURL(u)
}
func opener(target string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open", target}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", target}
	default:
		return []string{"xdg-open", target}
	}
}
func OpenURL(ctx context.Context, target string) error {
	if err := validateURL(target); err != nil {
		return err
	}
	r, err := run(ctx, opener(target), "", 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New("could not open the browser; check the system URL handler")
	}
	return nil
}
func OpenFile(ctx context.Context, path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	r, err := run(ctx, opener(path), "", 20*time.Second)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return errors.New("could not open file; check the system file handler")
	}
	return nil
}
