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
	waitWake(t, wake)
	value, err, ready := r.Poll()
	if !ready || err != nil || value != "current result" || r.Loading() {
		t.Fatalf("latest refresh result: %q %v %v", value, err, ready)
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
