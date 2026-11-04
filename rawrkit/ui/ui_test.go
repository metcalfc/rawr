package ui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

func simulation(t *testing.T) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(40, 8)
	t.Cleanup(s.Fini)
	return s
}
func runeKey(r rune) *tcell.EventKey       { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }
func namedKey(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }
func line(screen tcell.Screen, y int) string {
	width, _ := screen.Size()
	var text strings.Builder
	for x := 0; x < width; x++ {
		r, _, _, _ := screen.GetContent(x, y)
		text.WriteRune(r)
	}
	return strings.TrimRight(text.String(), " ")
}

type frame struct {
	count, selected, offset, activations int
	firstRow                             string
}

// paletteView is an unrelated fourth app: its data and actions are plain color
// names. It imports only the public UI package and tcell, not any Rawr internals.
type paletteView struct {
	canvas    *ui.Canvas
	list      ui.List
	refresh   *ui.Refresh[[]string]
	load      func(context.Context) ([]string, error)
	items     []string
	activated []string
	frames    chan frame
	closed    bool
	initErr   error
}

func (v *paletteView) Init(c *ui.Canvas) error {
	v.canvas = c
	v.refresh = ui.NewRefresh[[]string](c.Wake)
	return v.initErr
}
func (v *paletteView) Refresh(ctx context.Context) { v.refresh.Start(ctx, v.load) }
func (v *paletteView) area() ui.Rect {
	width, height := v.canvas.Size()
	return ui.Rect{Y: 2, Width: width, Height: max(0, height-4)}
}
func (v *paletteView) Handle(ctx context.Context, event tcell.Event) bool {
	if key, ok := event.(*tcell.EventKey); ok {
		if key.Rune() == 'q' {
			return true
		}
		if key.Rune() == 'r' {
			v.Refresh(ctx)
			return false
		}
	}
	if _, activate := v.list.Handle(event, len(v.items), v.area()); activate {
		v.activated = append(v.activated, v.items[v.list.Selected])
	}
	return false
}
func (v *paletteView) Draw(c *ui.Canvas) {
	if items, err, ready := v.refresh.Poll(); ready && err == nil {
		v.items = items
	}
	c.Text(0, 0, "Palette", c.Theme.Title)
	v.list.Draw(c, v.area(), len(v.items), func(i int) string { return v.items[i] })
	if v.frames != nil {
		v.frames <- frame{len(v.items), v.list.Selected, v.list.Offset, len(v.activated), line(c.Screen, 2)}
	}
}
func (v *paletteView) Close() {
	v.closed = true
	if v.refresh != nil {
		v.refresh.Close()
	}
}
func awaitFrame(t *testing.T, frames <-chan frame, want func(frame) bool) frame {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case f := <-frames:
			if want(f) {
				return f
			}
		case <-timer.C:
			t.Fatal("UI did not render expected frame")
			return frame{}
		}
	}
}

func TestUnrelatedAppUsesRuntimeListScrollingAndOneActivationPerClick(t *testing.T) {
	s := simulation(t)
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf("color-%02d", i)
	}
	v := &paletteView{load: func(context.Context) ([]string, error) { return items, nil }, frames: make(chan frame, 32)}
	done := make(chan error, 1)
	go func() { done <- ui.RunScreen(context.Background(), s, v) }()
	awaitFrame(t, v.frames, func(f frame) bool { return f.count == 30 })
	_ = s.PostEvent(namedKey(tcell.KeyEnd))
	f := awaitFrame(t, v.frames, func(f frame) bool { return f.selected == 29 })
	if f.offset != 26 || f.firstRow != "color-26" || f.activations != 0 {
		t.Fatalf("list did not scroll independently of activation: %+v", f)
	}
	press := tcell.NewEventMouse(2, 2, tcell.Button1, tcell.ModNone)
	_ = s.PostEvent(press)
	f = awaitFrame(t, v.frames, func(f frame) bool { return f.activations == 1 })
	if f.selected != 26 {
		t.Fatalf("mouse did not map through viewport: %+v", f)
	}
	_ = s.PostEvent(press)
	_ = s.PostEvent(tcell.NewEventMouse(2, 2, tcell.ButtonNone, tcell.ModNone))
	_ = s.PostEvent(namedKey(tcell.KeyEnter))
	awaitFrame(t, v.frames, func(f frame) bool { return f.activations == 2 })
	_ = s.PostEvent(runeKey('q'))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("UI did not quit")
	}
	if !v.closed || len(v.activated) != 2 || v.activated[0] != "color-26" || v.activated[1] != "color-26" {
		t.Fatalf("held mouse reactivated or lifecycle failed: %+v", v.activated)
	}
}

func TestRuntimeCancellationAndCtrlCStopSlowRefresh(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint("context-cancel=", cancelled), func(t *testing.T) {
			s := simulation(t)
			started, stopped := make(chan struct{}), make(chan struct{})
			v := &paletteView{load: func(ctx context.Context) ([]string, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return nil, ctx.Err()
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ui.RunScreen(ctx, s, v) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("refresh not started")
			}
			if cancelled {
				cancel()
			} else {
				_ = s.PostEvent(namedKey(tcell.KeyCtrlC))
			}
			select {
			case err := <-done:
				if cancelled && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error: %v", err)
				}
				if !cancelled && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("slow refresh blocked quit")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("view's outstanding refresh not canceled")
			}
			if !v.closed {
				t.Fatal("view was not closed")
			}
		})
	}
}

func TestInitializationFailureClosesView(t *testing.T) {
	s := simulation(t)
	sentinel := errors.New("bad app context")
	v := &paletteView{initErr: sentinel}
	if err := ui.RunScreen(context.Background(), s, v); !errors.Is(err, sentinel) || !v.closed {
		t.Fatalf("initialization cleanup: closed=%v err=%v", v.closed, err)
	}
}

func TestSafeUnicodeAndWidgetClipping(t *testing.T) {
	s := simulation(t)
	canvas := ui.NewCanvas(s)
	canvas.Text(0, 0, "界 e\u0301 👾\x1b\u202e", canvas.Theme.Normal)
	r, _, _, width := s.GetContent(0, 0)
	if r != '界' || width != 2 {
		t.Fatalf("wide glyph was split: %q width=%d", r, width)
	}
	r, combining, _, _ := s.GetContent(3, 0)
	if r != 'e' || len(combining) != 1 || combining[0] != '\u0301' {
		t.Fatalf("combining glyph lost: %q %q", r, string(combining))
	}
	if got := ui.SafeText("界 é 👾\x1b\u202e\n"); got != "界 é 👾���" {
		t.Fatalf("unsafe text: %q", got)
	}
	s.SetContent(5, 2, '|', nil, canvas.Theme.Normal)
	list := ui.List{}
	list.Draw(canvas, ui.Rect{X: 2, Y: 2, Width: 3, Height: 1}, 1, func(int) string { return "界界" })
	r, _, _, _ = s.GetContent(5, 2)
	if r != '|' {
		t.Fatal("wide list row overwrote an adjacent widget")
	}
	canvas.Text(-1, 0, "outside", canvas.Theme.Normal)
	canvas.Text(0, -1, "outside", canvas.Theme.Normal)
}

func TestListIgnoresOutsideWheelAndNeverActivatesAnEmptyList(t *testing.T) {
	list := ui.List{}
	area := ui.Rect{X: 4, Y: 2, Width: 10, Height: 3}
	if handled, activate := list.Handle(tcell.NewEventMouse(1, 1, tcell.WheelDown, tcell.ModNone), 20, area); handled || activate || list.Selected != 0 {
		t.Fatal("outside wheel moved list")
	}
	if _, activate := list.Handle(namedKey(tcell.KeyEnter), 0, area); activate {
		t.Fatal("empty list activated")
	}
	list.Selected = 99
	list.Offset = 98
	list.Clamp(2, 3)
	if list.Selected != 1 || list.Offset != 0 {
		t.Fatalf("selection did not clamp after refresh: %+v", list)
	}
}

type suspendScreen struct {
	tcell.Screen
	suspendErr, resumeErr error
	suspended, resumed    int
}

func (s *suspendScreen) Suspend() error { s.suspended++; return s.suspendErr }
func (s *suspendScreen) Resume() error  { s.resumed++; return s.resumeErr }
func TestSuspendRestoresTheSameScreenOnActionError(t *testing.T) {
	base := simulation(t)
	actionErr, resumeErr := errors.New("shell exited"), errors.New("screen unavailable")
	s := &suspendScreen{Screen: base, resumeErr: resumeErr}
	canvas := ui.NewCanvas(s)
	err := canvas.Suspend(func() error { return actionErr })
	if !errors.Is(err, actionErr) || !errors.Is(err, resumeErr) || s.suspended != 1 || s.resumed != 1 {
		t.Fatalf("suspend lifecycle failed: %v %+v", err, s)
	}
}
