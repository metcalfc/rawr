// Package ui provides a small terminal UI runtime and reusable widgets.
// It has no knowledge of projects, pane placement, GitHub, files, or VMs.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// View implements an application's contents. Init runs before the initial
// Refresh. Handle returns true to quit; q and Escape remain app decisions so
// they can leave a detail view first. Ctrl-C and resize are handled by the runtime.
// Refresh should return promptly; Refresh[T] can perform asynchronous requests.
type View interface {
	Init(*Canvas) error
	Refresh(context.Context)
	Handle(context.Context, tcell.Event) bool
	Draw(*Canvas)
}

// Theme controls the shared frame and list styles. Applications may customize it
// from Init without implementing their own rendering or terminal lifecycle.
type Theme struct{ Normal, Title, Selected tcell.Style }

func DefaultTheme() Theme {
	normal := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDefault)
	return Theme{Normal: normal, Title: normal.Bold(true), Selected: tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorLightSkyBlue)}
}

// Canvas wraps the native screen. Screen is available for specialized widgets;
// Text always treats input as inert text rather than terminal instructions.
type Canvas struct {
	Screen tcell.Screen
	Theme  Theme
}

func NewCanvas(screen tcell.Screen) *Canvas { return &Canvas{Screen: screen, Theme: DefaultTheme()} }
func (c *Canvas) Size() (int, int)          { return c.Screen.Size() }
func (c *Canvas) Wake()                     { _ = c.Screen.PostEvent(tcell.NewEventInterrupt(nil)) }

// SafeText preserves printable Unicode while replacing controls and invisible
// formatting characters, including ESC and bidirectional control characters.
func SafeText(value string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, value)
}

// Text draws a clipped line at x,y, including wide and combining Unicode glyphs.
func (c *Canvas) Text(x, y int, value string, style tcell.Style) {
	width, _ := c.Size()
	c.textWidth(x, y, value, style, width-x)
}
func (c *Canvas) textWidth(x, y int, value string, style tcell.Style, limit int) {
	width, height := c.Size()
	if x < 0 || y < 0 || y >= height || x >= width || limit <= 0 {
		return
	}
	right := min(width, x+limit)
	runes := []rune(SafeText(value))
	for i := 0; i < len(runes) && x < right; i++ {
		r := runes[i]
		var combining []rune
		for i+1 < len(runes) && unicode.Is(unicode.Mn, runes[i+1]) {
			i++
			combining = append(combining, runes[i])
		}
		cellWidth := max(1, uniseg.StringWidth(string(append([]rune{r}, combining...))))
		if x+cellWidth > right {
			return
		}
		c.Screen.SetContent(x, y, r, combining, style)
		x += cellWidth
	}
}

// Suspend lends this same terminal to a foreground action, then restores the UI
// even if that action fails. It never creates another terminal or injects input.
func (c *Canvas) Suspend(action func() error) error {
	if err := c.Screen.Suspend(); err != nil {
		return err
	}
	actionErr := action()
	resumeErr := c.Screen.Resume()
	c.Screen.Sync()
	if resumeErr != nil {
		return errors.Join(actionErr, fmt.Errorf("cannot resume terminal UI: %w", resumeErr))
	}
	return actionErr
}

// Run owns the real terminal's initialization and restoration.
func Run(ctx context.Context, view View) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("cannot create terminal screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("cannot initialize terminal; run in an interactive terminal: %w", err)
	}
	defer screen.Fini()
	return RunScreen(ctx, screen, view)
}

// RunScreen serves an already initialized screen. The caller owns Init/Fini;
// this makes simulation screens and embedding possible without a real TTY.
// A view implementing Close() is closed on every initialized exit path.
func RunScreen(ctx context.Context, screen tcell.Screen, view View) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	canvas := NewCanvas(screen)
	if closer, ok := view.(interface{ Close() }); ok {
		defer closer.Close()
	}
	if err := view.Init(canvas); err != nil {
		return err
	}
	screen.EnableMouse(tcell.MouseButtonEvents)
	defer screen.DisableMouse()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			canvas.Wake()
		case <-done:
		}
	}()
	view.Refresh(ctx)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		screen.SetStyle(canvas.Theme.Normal)
		screen.Clear()
		view.Draw(canvas)
		screen.Show()
		event := screen.PollEvent()
		if event == nil {
			return nil
		}
		if key, ok := event.(*tcell.EventKey); ok && key.Key() == tcell.KeyCtrlC {
			return nil
		}
		if _, ok := event.(*tcell.EventResize); ok {
			screen.Sync()
		}
		if view.Handle(ctx, event) {
			return nil
		}
	}
}
