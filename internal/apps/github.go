package apps

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

type githubConfirmation struct {
	root, repo string
	pr         model.PR
}
type githubCheckData struct {
	repo   string
	number int
	checks []model.Check
	err    error
	loaded bool
}
type githubCheckoutData struct {
	repo   string
	number int
	err    error
}
type githubView struct {
	issues         bool
	filter         [2]string
	previousFilter string
	filtering      bool
	positions      [2]ui.List
	stale          bool
	confirm        *githubConfirmation
	busy           bool
	pending        *model.GitHubStatus
	checks         map[string]githubCheckData
	checkLoader    *ui.Refresh[githubCheckData]
	checking       int
	checkoutLoader *ui.Refresh[githubCheckoutData]
	checkoutDone   chan githubCheckoutData
	notice         string
	finalMessage   string
	finalError     error
}

func (g *githubView) mode() int {
	if g.issues {
		return 1
	}
	return 0
}
func (a *application) githubIndices() []int {
	query := strings.ToLower(strings.TrimSpace(a.gh.filter[a.gh.mode()]))
	indices := []int{}
	if a.gh.issues {
		for i, item := range a.github.Issues {
			if query == "" || strings.Contains(strings.ToLower(fmt.Sprintf("#%d %s", item.Number, item.Title)), query) {
				indices = append(indices, i)
			}
		}
	} else {
		for i, item := range a.github.PRs {
			if query == "" || strings.Contains(strings.ToLower(fmt.Sprintf("#%d %s", item.Number, item.Title)), query) {
				indices = append(indices, i)
			}
		}
	}
	return indices
}
func (a *application) githubCount() int { return len(a.githubIndices()) }
func (a *application) githubPR() (model.PR, bool) {
	indices := a.githubIndices()
	if a.gh.issues || a.list.Selected < 0 || a.list.Selected >= len(indices) {
		return model.PR{}, false
	}
	return a.github.PRs[indices[a.list.Selected]], true
}
func (a *application) githubNumber() int {
	indices := a.githubIndices()
	if a.list.Selected < 0 || a.list.Selected >= len(indices) {
		return 0
	}
	if a.gh.issues {
		return a.github.Issues[indices[a.list.Selected]].Number
	}
	return a.github.PRs[indices[a.list.Selected]].Number
}
func (a *application) githubSelectNumber(number int) {
	a.list.Selected = 0
	for row, index := range a.githubIndices() {
		n := 0
		if a.gh.issues {
			n = a.github.Issues[index].Number
		} else {
			n = a.github.PRs[index].Number
		}
		if n == number {
			a.list.Selected = row
			break
		}
	}
	a.list.Clamp(a.githubCount(), a.area().Height)
}

// githubApply preserves cached categories if that category failed to refresh.
// Repository identity is chosen once, not inferred from subsequent focus changes.
func (a *application) githubApply(status model.GitHubStatus) {
	if a.gh.confirm != nil || a.gh.busy {
		copy := status
		a.gh.pending = &copy
		return
	}
	repo := a.context.Repository
	if repo == "" {
		repo = a.github.Repository
	}
	if repo != "" && status.Repository != "" && !strings.EqualFold(repo, status.Repository) {
		a.message = "Repository changed during refresh; cached project data retained."
		a.gh.stale = true
		a.status.Fail(fmt.Errorf("%s", a.message))
		return
	}
	if a.context.Repository == "" && status.Repository != "" {
		a.context.Repository = status.Repository
	}
	if status.State == "error" {
		a.gh.stale = true
		if a.github.Repository == "" {
			a.github.Repository = status.Repository
		}
		a.message = "Refresh failed; cached data retained. " + status.Message
		a.status.Fail(fmt.Errorf("%s", a.message))
		return
	}
	selected := a.githubNumber()
	if status.PRsError != "" {
		status.PRs = a.github.PRs
	}
	if status.IssuesError != "" {
		status.Issues = a.github.Issues
	}
	a.github = status
	a.gh.stale = status.Partial || status.PRsError != "" || status.IssuesError != ""
	if a.gh.stale {
		a.status.Fail(fmt.Errorf("%s", status.Message))
	} else {
		a.status.Success()
	}
	a.githubSelectNumber(selected)
	if a.checkDetails && a.githubNumber() != selected {
		a.checkDetails = false
	}
	a.message = status.Message
	if a.gh.notice != "" {
		a.message = a.gh.notice
		if status.Message != "" {
			a.message += " · " + status.Message
		}
	}
}

func (a *application) githubOpen(ctx context.Context) {
	indices := a.githubIndices()
	if a.list.Selected < 0 || a.list.Selected >= len(indices) {
		a.message = "No selected GitHub item to open."
		return
	}
	u := ""
	if a.gh.issues {
		u = a.github.Issues[indices[a.list.Selected]].URL
	} else {
		u = a.github.PRs[indices[a.list.Selected]].URL
	}
	a.gh.notice = ""
	a.setError(a.deps.openURL(ctx, u))
}

func checkKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(repo), number)
}
func (a *application) githubCheckCache(pr model.PR) githubCheckData {
	key := checkKey(a.github.Repository, pr.Number)
	if a.gh.checks != nil {
		if value, ok := a.gh.checks[key]; ok {
			return value
		}
	}
	return githubCheckData{repo: a.github.Repository, number: pr.Number, checks: pr.Checks, loaded: pr.ChecksLoaded || pr.Checks != nil}
}
func (a *application) githubLoadChecks(ctx context.Context, force bool) {
	pr, ok := a.githubPR()
	if !ok {
		a.message = "Select a pull request to inspect its checks."
		return
	}
	a.checkDetails = true
	a.checkOffset = 0
	if cached := a.githubCheckCache(pr); cached.loaded && !force {
		return
	}
	if a.gh.checkLoader == nil {
		a.gh.checkLoader = ui.NewRefresh[githubCheckData](a.canvas.Wake)
	}
	checker := a.deps.checks
	if checker == nil {
		checker = services.GitHubChecks
	}
	root, repo := a.context.Root, a.github.Repository
	a.gh.checking = pr.Number
	a.message = "Refreshing selected PR checks…"
	a.gh.checkLoader.Start(ctx, func(ctx context.Context) (githubCheckData, error) {
		checks, err := checker(ctx, root, repo, pr)
		return githubCheckData{repo: repo, number: pr.Number, checks: checks, loaded: err == nil, err: err}, err
	})
}
func (a *application) githubPollChecks() {
	if a.gh.checkLoader == nil {
		return
	}
	result, err, ready := a.gh.checkLoader.Poll()
	if !ready {
		return
	}
	if a.gh.checks == nil {
		a.gh.checks = map[string]githubCheckData{}
	}
	key := checkKey(result.repo, result.number)
	if err != nil {
		old := a.gh.checks[key]
		if old.number == 0 {
			for _, pr := range a.github.PRs {
				if pr.Number == result.number {
					old = a.githubCheckCache(pr)
					break
				}
			}
		}
		old.repo, old.number, old.err = result.repo, result.number, err
		a.gh.checks[key] = old
		a.message = "Checks refresh failed; previous checks retained. " + err.Error()
	} else {
		a.gh.checks[key] = result
		a.message = ""
	}
}

func (a *application) githubBeginCheckout() {
	pr, ok := a.githubPR()
	if !ok || pr.State != "OPEN" {
		a.message = "Select an open pull request to check out."
		return
	}
	if a.callbacks.CheckoutPR == nil {
		a.message = "Checkout is unavailable in this invocation."
		return
	}
	pr = model.PR{Number: pr.Number, Title: pr.Title, URL: pr.URL, State: pr.State}
	a.gh.confirm = &githubConfirmation{root: a.context.Root, repo: a.github.Repository, pr: pr}
	a.message = "Confirm checkout for this repository and PR with y; n cancels."
}
func (a *application) githubStartCheckout(ctx context.Context) {
	confirmation := a.gh.confirm
	if confirmation == nil || a.gh.busy {
		return
	}
	if confirmation.root != a.context.Root || !strings.EqualFold(confirmation.repo, a.github.Repository) || !strings.EqualFold(confirmation.repo, a.context.Repository) {
		a.gh.confirm = nil
		a.message = "Project identity changed; checkout canceled."
		return
	}
	if a.gh.checkoutLoader == nil {
		a.gh.checkoutLoader = ui.NewRefresh[githubCheckoutData](a.canvas.Wake)
	}
	callback := a.callbacks.CheckoutPR
	if callback == nil {
		a.gh.confirm = nil
		a.message = "Checkout is unavailable in this invocation."
		return
	}
	done := make(chan githubCheckoutData, 1)
	a.gh.checkoutDone = done
	a.gh.busy = true
	a.message = fmt.Sprintf("Checking out %s PR #%d; waiting for the result…", confirmation.repo, confirmation.pr.Number)
	a.gh.checkoutLoader.Start(ctx, func(context.Context) (githubCheckoutData, error) {
		result := githubCheckoutData{repo: confirmation.repo, number: confirmation.pr.Number, err: callback(confirmation.pr)}
		done <- result
		return result, result.err
	})
}
func (a *application) githubPollCheckout(ctx context.Context) {
	if !a.gh.busy || a.gh.checkoutLoader == nil {
		return
	}
	_, _, ready := a.gh.checkoutLoader.Poll()
	if !ready {
		return
	}
	result := <-a.gh.checkoutDone
	a.gh.busy = false
	a.gh.confirm = nil
	if result.err != nil {
		a.gh.notice = "Checkout failed; inspect HEAD/status before retrying. " + result.err.Error()
	} else {
		a.gh.notice = fmt.Sprintf("Checked out %s PR #%d in the anchored worktree.", result.repo, result.number)
	}
	if a.gh.pending != nil {
		pending := *a.gh.pending
		a.gh.pending = nil
		a.githubApply(pending)
	}
	a.message = a.gh.notice
	if result.err == nil {
		a.refresh(ctx)
	}
}

// githubClose finishes an already confirmed bounded operation before discarding
// UI resources. Its outcome is reported after the terminal screen is restored.
func (a *application) githubClose() {
	if a.gh.busy {
		result := <-a.gh.checkoutDone
		a.gh.busy = false
		a.gh.finalError = result.err
		if result.err != nil {
			a.gh.finalMessage = fmt.Sprintf("Checkout %s PR #%d failed; inspect HEAD/status before retrying: %v", result.repo, result.number, result.err)
		} else {
			a.gh.finalMessage = fmt.Sprintf("Checked out %s PR #%d in the anchored worktree.", result.repo, result.number)
		}
	}
	if a.gh.checkLoader != nil {
		a.gh.checkLoader.Close()
	}
	if a.gh.checkoutLoader != nil {
		a.gh.checkoutLoader.Close()
	}
}
func (a *application) githubFinalOutcome() (string, error) { return a.gh.finalMessage, a.gh.finalError }

func (a *application) githubHandle(ctx context.Context, event tcell.Event) bool {
	a.githubPollChecks()
	a.githubPollCheckout(ctx)
	if key, ok := event.(*tcell.EventKey); ok && key.Key() == tcell.KeyCtrlC {
		return true
	}
	if a.gh.busy {
		a.message = "Checkout is running; its result will be retained. Please wait."
		return false
	}
	if a.gh.confirm != nil {
		if key, ok := event.(*tcell.EventKey); ok {
			if key.Rune() == 'y' {
				a.githubStartCheckout(ctx)
			} else if key.Rune() == 'n' || key.Rune() == 'q' || key.Key() == tcell.KeyEscape {
				a.gh.confirm = nil
				a.message = "Checkout canceled."
				if a.gh.pending != nil {
					pending := *a.gh.pending
					a.gh.pending = nil
					a.githubApply(pending)
				}
			}
		}
		return false
	}
	if a.gh.filtering {
		key, ok := event.(*tcell.EventKey)
		if !ok {
			return false
		}
		mode := a.gh.mode()
		text := []rune(a.gh.filter[mode])
		switch key.Key() {
		case tcell.KeyEscape:
			a.gh.filter[mode] = a.gh.previousFilter
			a.gh.filtering = false
		case tcell.KeyEnter:
			a.gh.filtering = false
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if len(text) > 0 {
				a.gh.filter[mode] = string(text[:len(text)-1])
			}
		case tcell.KeyCtrlU:
			a.gh.filter[mode] = ""
		case tcell.KeyRune:
			if len(text) < 128 {
				a.gh.filter[mode] += string(key.Rune())
			}
		}
		a.list.Selected = 0
		a.list.Clamp(a.githubCount(), a.area().Height)
		return false
	}
	if key, ok := event.(*tcell.EventKey); ok {
		if key.Rune() == 'q' || key.Key() == tcell.KeyEscape {
			if a.checkDetails {
				a.checkDetails = false
				return false
			}
			return true
		}
		if key.Rune() == '1' || key.Rune() == '2' {
			a.gh.positions[a.gh.mode()] = a.list
			a.gh.issues = key.Rune() == '2'
			a.list = a.gh.positions[a.gh.mode()]
			a.checkDetails = false
			a.list.Clamp(a.githubCount(), a.area().Height)
			return false
		}
		if key.Rune() == '/' {
			a.gh.previousFilter = a.gh.filter[a.gh.mode()]
			a.gh.filtering = true
			a.checkDetails = false
			return false
		}
		if key.Rune() == 'x' {
			a.githubBeginCheckout()
			return false
		}
		if key.Rune() == 'c' {
			if a.checkDetails {
				a.checkDetails = false
			} else {
				a.githubLoadChecks(ctx, false)
			}
			return false
		}
		if key.Rune() == 'r' {
			a.gh.notice = ""
			a.refresh(ctx)
			if a.checkDetails {
				a.githubLoadChecks(ctx, true)
			}
			return false
		}
		if key.Key() == tcell.KeyEnter || key.Rune() == 'o' {
			a.githubOpen(ctx)
			return false
		}
	}
	if a.checkDetails {
		if pr, ok := a.githubPR(); ok {
			document := ui.Document{Offset: a.checkOffset}
			document.Handle(event, len(a.githubCheckCache(pr).checks), max(1, a.area().Height-2))
			a.checkOffset = document.Offset
		}
		return false
	}
	a.list.Handle(event, a.githubCount(), a.area()) // Mouse and arrows browse; browser/checkout are explicit keys.
	return false
}

func (a *application) githubDraw(last int) {
	a.githubPollChecks()
	style := a.canvas.Theme
	mode := fmt.Sprintf("[1 PRs] %d · 2 Issues %d", len(a.github.PRs), len(a.github.Issues))
	if a.gh.issues {
		mode = fmt.Sprintf("1 PRs %d · [2 Issues] %d", len(a.github.PRs), len(a.github.Issues))
	}
	filter := a.gh.filter[a.gh.mode()]
	bar := fmt.Sprintf("%s · %d matching · / %s", mode, a.githubCount(), filter)
	if a.gh.filtering {
		bar = "Filter: /" + filter + "_"
	}
	if a.gh.stale {
		bar += " · STALE"
	}
	a.canvas.Text(0, contentStart-1, bar, style.Title)
	if a.gh.confirm != nil {
		c := a.gh.confirm
		a.canvas.Text(0, contentStart, fmt.Sprintf("Check out PR #%d?", c.pr.Number), style.Title)
		a.canvas.Text(0, contentStart+1, c.pr.Title, style.Normal)
		a.canvas.Text(0, contentStart+3, "Repository: "+c.repo, style.Normal)
		a.canvas.Text(0, contentStart+4, "Worktree: "+c.root, style.Normal)
		if a.gh.busy {
			a.canvas.Text(0, contentStart+6, "Checkout running; preserving its final outcome…", style.Title)
		} else {
			a.canvas.Text(0, contentStart+6, "y checks out this PR · n cancels", style.Title)
		}
		return
	}
	if a.checkDetails {
		pr, ok := a.githubPR()
		if !ok {
			return
		}
		a.canvas.Text(0, contentStart, fmt.Sprintf("#%d %s", pr.Number, pr.Title), style.Title)
		cached := a.githubCheckCache(pr)
		if cached.err != nil {
			a.canvas.Text(0, contentStart+1, "STALE checks: "+cached.err.Error(), style.Normal)
		} else if a.gh.checkLoader != nil && a.gh.checkLoader.Loading() {
			a.canvas.Text(0, contentStart+1, "Refreshing checks…", style.Normal)
		} else if cached.loaded && len(cached.checks) == 0 {
			a.canvas.Text(0, contentStart+1, "No checks reported for this pull request.", style.Normal)
		}
		lines := make([]string, 0, len(cached.checks))
		for _, check := range cached.checks {
			lines = append(lines, check.Bucket+" · "+check.Name+" · "+check.State)
		}
		document := ui.Document{Offset: a.checkOffset}
		area := a.area()
		area.Y = contentStart + 2
		area.Height = max(0, last-area.Y)
		document.Draw(a.canvas, area, lines)
		a.checkOffset = document.Offset
		return
	}
	indices := a.githubIndices()
	area := a.area()
	area.Height = max(0, last-contentStart)
	a.list.Draw(a.canvas, area, len(indices), func(row int) string {
		index := indices[row]
		if a.gh.issues {
			item := a.github.Issues[index]
			return fmt.Sprintf("#%d %s", item.Number, item.Title)
		}
		item := a.github.PRs[index]
		return fmt.Sprintf("#%d %s", item.Number, item.Title)
	})
	if len(indices) == 0 {
		message := "No matching open pull requests."
		if a.gh.issues {
			message = "No matching open issues."
		}
		if a.gh.stale {
			message = "No matching cached items; refresh needed."
		}
		a.canvas.Text(0, contentStart, message, style.Normal)
	}
}
func (a *application) githubHints() string {
	if a.gh.busy {
		return "Checkout running · result retained · no automatic retry"
	}
	if a.gh.confirm != nil {
		return "y checkout this PR · n/Esc cancel"
	}
	if a.gh.filtering {
		return "Type filter · Enter apply · Esc cancel · Ctrl-U clear"
	}
	if a.checkDetails {
		return "Enter web · ? help · x checkout · c/q back · ↑↓ scroll · r refresh"
	}
	if a.gh.issues {
		return "Enter web · ? help · / filter · r refresh · q quit"
	}
	return "Enter web · ? help · x checkout · c checks · / filter · r refresh"
}
