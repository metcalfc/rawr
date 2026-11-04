package apps

import (
	"context"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/internal/model"
)

func TestDeferredPartialRefreshMarksCachedDataStaleAfterConfirmation(t *testing.T) {
	a, _ := testApplication(t, "ghrawr", model.Context{Repository: "owner/repo"}, model.Callbacks{CheckoutPR: func(model.PR) error { return nil }})
	a.githubApply(model.GitHubStatus{State: "ready", Repository: "owner/repo", PRs: []model.PR{{Number: 42, State: "OPEN", URL: "https://github.com/owner/repo/pull/42"}}})
	updated := a.status.Updated
	a.githubBeginCheckout()
	a.githubApply(model.GitHubStatus{State: "partial", Repository: "owner/repo", Partial: true, PRsError: "offline", Message: "PR list unavailable"})
	if a.status.Error != "" {
		t.Fatal("queued refresh changed the visible confirmation status")
	}
	a.githubHandle(context.Background(), character('n'))
	if !a.gh.stale || a.status.Error == "" || !a.status.Updated.Equal(updated) || len(a.github.PRs) != 1 {
		t.Fatal("deferred refresh lost cached rows or their stale indicator")
	}
}

func TestNarrowHelpPreservesFullActionError(t *testing.T) {
	a, s := testApplication(t, "exerawr", model.Context{}, model.Callbacks{})
	s.SetSize(28, 12)
	a.message = "The foreground program cannot be verified; return to a directly hosted local shell."
	lines := a.helpLines()
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, a.message) {
		t.Fatal("full action error is inaccessible in narrow help")
	}
	for _, line := range lines {
		if len([]rune(line)) > 28 {
			t.Fatal("help line exceeds the narrow viewport")
		}
	}
}
