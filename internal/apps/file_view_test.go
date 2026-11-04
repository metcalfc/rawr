package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

func awaitFileView(t *testing.T, v *fileDocumentView) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for v.loader.Loading() {
		v.poll()
		if time.Now().After(deadline) {
			t.Fatal("file view did not finish loading")
		}
		time.Sleep(time.Millisecond)
	}
}
func initFileView(t *testing.T, v *fileDocumentView) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 14)
	t.Cleanup(screen.Fini)
	if err := v.Init(ui.NewCanvas(screen)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return screen
}

func TestFileViewInitialReadFailureIsVisibleAndReloadable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "raced-away.txt")
	v, err := newFileDocumentView(context.Background(), root, path, false, false)
	if err != nil {
		t.Fatal("initial file read killed viewer", err)
	}
	screen := initFileView(t, v)
	v.Refresh(context.Background())
	awaitFileView(t, v)
	if v.status.Error == "" || len(v.lines) != 0 {
		t.Fatal(v.status, v.lines)
	}
	v.Draw(v.canvas)
	if !strings.Contains(screenLine(screen, 3), "retry") {
		t.Fatal(screenLine(screen, 3))
	}
	if err = os.WriteFile(path, []byte("reappeared\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v.Handle(context.Background(), character('r'))
	awaitFileView(t, v)
	if v.status.Error != "" || len(v.lines) == 0 || v.lines[0] != "reappeared" {
		t.Fatal(v.status, v.lines)
	}
}

func TestFileViewInitialDiffFailureKeepsAUsableFrame(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "not-git.txt")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := newFileDocumentView(context.Background(), root, path, true, false)
	if err != nil {
		t.Fatal(err)
	}
	initFileView(t, v)
	v.Refresh(context.Background())
	awaitFileView(t, v)
	if v.status.Error == "" || len(v.lines) != 0 {
		t.Fatal(v.status, v.lines)
	}
	v.Draw(v.canvas)
	if !v.Handle(context.Background(), character('q')) {
		t.Fatal("failed diff viewer could not quit")
	}
}

func TestFileViewLoadsOffUIThreadAndCloseCancels(t *testing.T) {
	v, err := newFileDocumentView(context.Background(), t.TempDir(), "fixture", false, false)
	if err != nil {
		t.Fatal(err)
	}
	screen := initFileView(t, v)
	started, canceled := make(chan struct{}), make(chan struct{})
	v.read = func(ctx context.Context, _ string, _ string, _ bool, _ bool) (services.FileDocument, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return services.FileDocument{}, ctx.Err()
	}
	v.Refresh(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("loader did not start")
	}
	v.Draw(v.canvas)
	if !strings.Contains(screenLine(screen, 3), "Loading") {
		t.Fatal("frame blocked or missing loading status")
	}
	if !v.Handle(context.Background(), character('q')) {
		t.Fatal("slow load blocked quit")
	}
	v.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel file loading")
	}
}
