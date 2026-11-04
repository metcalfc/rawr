package apps

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
)

func githubFixture(t *testing.T, callbacks model.Callbacks) (*application, tcell.SimulationScreen) {
	t.Helper()
	a, screen := testApplication(t, "ghrawr", model.Context{Repository: "owner/repo"}, callbacks)
	status := model.GitHubStatus{State: "ready", Repository: "owner/repo", Branch: "main", PRs: []model.PR{
		{Number: 42, Title: "Improve native panes", State: "OPEN", URL: "https://github.com/owner/repo/pull/42"},
		{Number: 7, Title: "Fix focus", State: "OPEN", URL: "https://github.com/owner/repo/pull/7"},
	}, Issues: []model.Issue{
		{Number: 4, Title: "Crash on startup", State: "OPEN", URL: "https://github.com/owner/repo/issues/4"},
		{Number: 6, Title: "Document shortcuts", State: "OPEN", URL: "https://github.com/owner/repo/issues/6"},
	}}
	a.githubApply(status)
	a.deps.github = func(context.Context, string, string) model.GitHubStatus { return status }
	return a, screen
}
func awaitGitHubChecks(t *testing.T, a *application) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for a.gh.checkLoader.Loading() {
		a.githubPollChecks()
		if time.Now().After(deadline) {
			t.Fatal("selected checks did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
func awaitCheckout(t *testing.T, a *application) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for a.gh.busy {
		a.githubPollCheckout(context.Background())
		if time.Now().After(deadline) {
			t.Fatal("checkout did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGitHubRepositoryModesFilterAndExplicitBrowserActions(t *testing.T) {
	a, screen := githubFixture(t, model.Callbacks{})
	opened := []string{}
	a.deps.openURL = func(_ context.Context, u string) error { opened = append(opened, u); return nil }
	a.draw()
	a.handle(context.Background(), tcell.NewEventMouse(1, contentStart+1, tcell.Button1, tcell.ModNone))
	a.handle(context.Background(), tcell.NewEventMouse(1, contentStart+1, tcell.ButtonNone, tcell.ModNone))
	if a.githubNumber() != 7 || len(opened) != 0 {
		t.Fatal("mouse browsing performed an action")
	}
	a.handle(context.Background(), character('2'))
	a.handle(context.Background(), character('/'))
	for _, r := range "crash" {
		a.handle(context.Background(), character(r))
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	if a.githubCount() != 1 || len(opened) != 0 {
		t.Fatal("filter entry opened an item or failed to narrow issues")
	}
	a.draw()
	if !strings.Contains(screenLine(screen, contentStart), "Crash on startup") {
		t.Fatal("issues were not rendered")
	}
	a.handle(context.Background(), key(tcell.KeyEnter))
	a.handle(context.Background(), character('1'))
	a.handle(context.Background(), character('o'))
	if !reflect.DeepEqual(opened, []string{"https://github.com/owner/repo/issues/4", "https://github.com/owner/repo/pull/7"}) {
		t.Fatalf("wrong selected URLs: %v", opened)
	}
}

func TestGitHubFailedRefreshRetainsDataAndPartialKeepsFailedCategory(t *testing.T) {
	a, screen := githubFixture(t, model.Callbacks{})
	before := a.github
	a.githubApply(model.GitHubStatus{State: "error", Repository: "owner/repo", Message: "offline"})
	if !reflect.DeepEqual(a.github, before) || !a.gh.stale {
		t.Fatal("failed refresh erased usable repository data")
	}
	a.draw()
	if !strings.Contains(screenLine(screen, contentStart-1), "STALE") {
		t.Fatal("stale data not labeled")
	}
	a.githubApply(model.GitHubStatus{State: "partial", Repository: "owner/repo", PRs: []model.PR{}, Partial: true, IssuesError: "offline"})
	if len(a.github.PRs) != 0 || len(a.github.Issues) != 2 {
		t.Fatal("partial refresh discarded the failed category")
	}
	current := a.github
	a.githubApply(model.GitHubStatus{State: "ready", Repository: "other/repo"})
	if !reflect.DeepEqual(a.github, current) || a.context.Repository != "owner/repo" {
		t.Fatal("refresh changed anchored repository identity")
	}
}

func TestGitHubSelectedChecksAreAsyncAndFailuresKeepPreviousChecks(t *testing.T) {
	a, screen := githubFixture(t, model.Callbacks{})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a.deps.checks = func(ctx context.Context, root, repo string, pr model.PR) ([]model.Check, error) {
		calls.Add(1)
		if root != a.context.Root || repo != "owner/repo" || pr.Number != 42 {
			t.Error("selected checks lost anchored identity")
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []model.Check{{Name: "build", Bucket: "pending", State: "QUEUED"}}, nil
	}
	a.draw()
	if calls.Load() != 0 {
		t.Fatal("render fetched every PR's checks")
	}
	a.handle(context.Background(), character('c'))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("checks did not start")
	}
	if !a.gh.checkLoader.Loading() {
		t.Fatal("request blocked the event loop")
	}
	close(release)
	awaitGitHubChecks(t, a)
	pr, _ := a.githubPR()
	if got := a.githubCheckCache(pr); len(got.checks) != 1 || !got.loaded {
		t.Fatal("selected checks not cached")
	}
	a.deps.checks = func(context.Context, string, string, model.PR) ([]model.Check, error) {
		return nil, errors.New("network offline")
	}
	a.handle(context.Background(), character('r'))
	awaitGitHubChecks(t, a)
	if got := a.githubCheckCache(pr); len(got.checks) != 1 || got.err == nil {
		t.Fatal("failed refresh lost previous check rows")
	}
	a.draw()
	if !strings.Contains(screenLine(screen, contentStart+1), "STALE") {
		t.Fatal("stale check results not labeled")
	}
}

func TestGitHubCheckoutConfirmationFreezesPRAndRunsOnce(t *testing.T) {
	started, release := make(chan model.PR, 1), make(chan struct{})
	var calls atomic.Int32
	a, _ := githubFixture(t, model.Callbacks{CheckoutPR: func(pr model.PR) error { calls.Add(1); started <- pr; <-release; return nil }})
	a.handle(context.Background(), character('x'))
	if calls.Load() != 0 || a.gh.confirm == nil {
		t.Fatal("checkout bypassed confirmation")
	}
	a.handle(context.Background(), key(tcell.KeyDown))
	a.handle(context.Background(), character('2'))
	a.handle(context.Background(), character('r'))
	pending := a.github
	pending.Branch = "updated-while-confirming"
	pending.PRs = []model.PR{pending.PRs[1], pending.PRs[0]}
	a.githubApply(pending)
	if a.githubNumber() != 42 || a.gh.issues || a.github.Branch != "main" {
		t.Fatal("confirmation changed identity during interaction/refresh")
	}
	a.handle(context.Background(), character('y'))
	var pr model.PR
	select {
	case pr = <-started:
	case <-time.After(time.Second):
		t.Fatal("confirmed checkout did not start")
	}
	if pr.Number != 42 {
		t.Fatalf("wrong confirmed PR: %+v", pr)
	}
	a.handle(context.Background(), character('y'))
	a.handle(context.Background(), character('x'))
	if !a.gh.busy || calls.Load() != 1 {
		t.Fatal("busy checkout was duplicated or canceled")
	}
	close(release)
	awaitCheckout(t, a)
	if a.gh.confirm != nil || calls.Load() != 1 || !strings.Contains(a.gh.notice, "Checked out owner/repo PR #42") {
		t.Fatalf("checkout outcome lost: %q", a.gh.notice)
	}
}

func TestGitHubCheckoutCancellationAndIdentityDriftDoNotRunAction(t *testing.T) {
	called := 0
	a, _ := githubFixture(t, model.Callbacks{CheckoutPR: func(model.PR) error { called++; return nil }})
	a.handle(context.Background(), character('x'))
	a.handle(context.Background(), character('n'))
	if a.gh.confirm != nil || called != 0 {
		t.Fatal("n did not cancel confirmation")
	}
	a.handle(context.Background(), character('x'))
	a.context.Root = t.TempDir()
	a.handle(context.Background(), character('y'))
	if a.gh.confirm != nil || called != 0 {
		t.Fatal("identity drift was allowed to check out")
	}
}

func TestGitHubCanceledUIFinishesAndRetainsCheckoutOutcome(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	sentinel := errors.New("provider rejected checkout")
	a, _ := githubFixture(t, model.Callbacks{CheckoutPR: func(model.PR) error { close(started); <-release; return sentinel }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.handle(ctx, character('x'))
	a.handle(ctx, character('y'))
	<-started
	done := make(chan error, 1)
	go func() { done <- a.loop(ctx) }()
	cancel()
	select {
	case <-done:
		t.Fatal("UI close discarded the running checkout")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UI cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UI did not finish after checkout returned")
	}
	message, err := a.githubFinalOutcome()
	if !errors.Is(err, sentinel) || !strings.Contains(message, "owner/repo PR #42") || !strings.Contains(message, "inspect HEAD/status") {
		t.Fatalf("outcome was lost on cancellation: %q %v", message, err)
	}
}
