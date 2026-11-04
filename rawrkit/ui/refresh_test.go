package ui_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metcalfc/rawr/rawrkit/ui"
)

func waitWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("refresh did not wake UI")
	}
}

// Wakes are notifications that some worker enqueued a result, not an identity
// for the current generation. A superseded worker can race cancellation and
// enqueue one last stale result; Poll correctly discards it. Keep consuming
// notifications until the current result is actually available.
func waitRefreshResult[T any](t *testing.T, r *ui.Refresh[T], wake <-chan struct{}) (T, error) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		if value, err, ready := r.Poll(); ready {
			return value, err
		}
		select {
		case <-wake:
		case <-timer.C:
			t.Fatal("current refresh result did not arrive")
			var zero T
			return zero, nil
		}
	}
}
func TestRefreshDropsQueuedStaleResultsAndCancelsPreviousRequest(t *testing.T) {
	wake := make(chan struct{}, 8)
	r := ui.NewRefresh[string](func() { wake <- struct{}{} })
	defer r.Close()
	r.Start(context.Background(), func(context.Context) (string, error) { return "old result", nil })
	waitWake(t, wake) // Old data is now queued but deliberately not consumed.
	started, stopped := make(chan struct{}), make(chan struct{})
	r.Start(context.Background(), func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return "late result", nil
	})
	<-started
	if _, _, ready := r.Poll(); ready || !r.Loading() {
		t.Fatal("stale result replaced the pending refresh")
	}
	r.Start(context.Background(), func(context.Context) (string, error) { return "current result", nil })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("superseded request was not canceled")
	}
	value, err := waitRefreshResult(t, r, wake)
	if err != nil || value != "current result" || r.Loading() {
		t.Fatalf("latest refresh result: %q %v", value, err)
	}
}

func TestRefreshDeliversCurrentCancellationAndErrors(t *testing.T) {
	wake := make(chan struct{}, 8)
	r := ui.NewRefresh[string](func() { wake <- struct{}{} })
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx, func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() })
	cancel()
	waitWake(t, wake)
	_, err, ready := r.Poll()
	if !ready || !errors.Is(err, context.Canceled) || r.Loading() {
		t.Fatalf("latest cancellation stayed pending: %v %v", err, ready)
	}
	sentinel := errors.New("service offline")
	r.Start(context.Background(), func(context.Context) (string, error) { return "", sentinel })
	waitWake(t, wake)
	_, err, ready = r.Poll()
	if !ready || !errors.Is(err, sentinel) || r.Loading() {
		t.Fatalf("service error not delivered: %v %v", err, ready)
	}
}

func TestCloseCancelsAndInvalidatesPendingResult(t *testing.T) {
	wake := make(chan struct{}, 2)
	r := ui.NewRefresh[string](func() { wake <- struct{}{} })
	r.Start(context.Background(), func(context.Context) (string, error) { return "queued", nil })
	waitWake(t, wake)
	r.Close()
	r.Close()
	if _, _, ready := r.Poll(); ready || r.Loading() {
		t.Fatal("closed refresh exposed stale data")
	}
}
