package apps

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

// RunFileView is a bounded, read-only terminal viewer. Rex owns the native
// terminal hosting it; it never sends commands to another terminal.
func RunFileView(ctx context.Context, root, path string, diff, staged bool) error {
	v, err := newFileDocumentView(ctx, root, path, diff, staged)
	if err != nil {
		return err
	}
	return ui.Run(ctx, v)
}

type fileDocumentView struct {
	root, path   string
	diff, staged bool
	canvas       *ui.Canvas
	document     ui.Document
	lines        []string
	status       ui.ResourceStatus
	message      string
	loader       *ui.Refresh[services.FileDocument]
	read         func(context.Context, string, string, bool, bool) (services.FileDocument, error)
}

func newFileDocumentView(_ context.Context, root, path string, diff, staged bool) (*fileDocumentView, error) {
	// Native creation has already validated its request, but the file can race
	// that check. Keep an inspectable process alive while every read validates
	// again; an initial data error becomes a reloadable view, not a failed launch.
	return &fileDocumentView{root: root, path: path, diff: diff, staged: staged, read: services.ReadFileDocument}, nil
}
func (v *fileDocumentView) poll() {
	if v.loader == nil {
		return
	}
	doc, err, ready := v.loader.Poll()
	if !ready {
		return
	}
	if err != nil {
		v.status.Fail(err)
		v.message = err.Error()
		return
	}
	v.lines = safeFileLines(doc.Lines)
	v.path = doc.Path
	v.status.Success()
	v.message = ""
}
func (v *fileDocumentView) Init(canvas *ui.Canvas) error {
	v.canvas = canvas
	if v.loader != nil {
		v.loader.Close()
	}
	v.loader = ui.NewRefresh[services.FileDocument](canvas.Wake)
	return nil
}
func (v *fileDocumentView) Refresh(ctx context.Context) {
	if v.loader == nil {
		return
	}
	root, path, diff, staged, read := v.root, v.path, v.diff, v.staged, v.read
	if read == nil {
		read = services.ReadFileDocument
	}
	v.message = ""
	v.loader.Start(ctx, func(ctx context.Context) (services.FileDocument, error) { return read(ctx, root, path, diff, staged) })
}
func (v *fileDocumentView) Close() {
	if v.loader != nil {
		v.loader.Close()
	}
}
func (v *fileDocumentView) Handle(ctx context.Context, event tcell.Event) bool {
	v.poll()
	if key, ok := event.(*tcell.EventKey); ok {
		if key.Key() == tcell.KeyEscape || key.Rune() == 'q' {
			return true
		}
		if key.Rune() == 'r' {
			v.Refresh(ctx)
			return false
		}
	}
	_, height := v.canvas.Size()
	v.document.Handle(event, len(v.lines), max(0, height-5))
	return false
}
func (v *fileDocumentView) Draw(canvas *ui.Canvas) {
	v.poll()
	width, height := canvas.Size()
	title := "File · read only"
	if v.diff {
		title = "Diff · read only"
		if v.staged {
			title = "Staged diff · read only"
		}
	}
	canvas.Text(0, 0, title, canvas.Theme.Title)
	canvas.Text(0, 1, v.path, canvas.Theme.Normal)
	loading := v.loader != nil && v.loader.Loading()
	canvas.Text(0, 2, v.status.Line(loading), canvas.Theme.Normal)
	if len(v.lines) == 0 {
		message := "Unable to load this view. Press r to retry."
		if loading {
			message = "Loading read-only content…"
		}
		canvas.Text(0, 3, message, canvas.Theme.Normal)
	}
	v.document.Draw(canvas, ui.Rect{Y: 3, Width: width, Height: max(0, height-5)}, v.lines)
	canvas.Text(0, height-2, v.message, canvas.Theme.Title)
	canvas.Text(0, height-1, fmt.Sprintf("↑↓/PgUp/PgDn scroll · r reload · q quit · %d lines", len(v.lines)), canvas.Theme.Normal)
}
