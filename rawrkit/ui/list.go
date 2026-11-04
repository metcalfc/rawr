package ui

import "github.com/gdamore/tcell/v2"

// Rect is a widget's screen area. Width and Height are cell counts.
type Rect struct{ X, Y, Width, Height int }

// List supplies selection, viewport scrolling and one activation per mouse
// press. Its zero value is ready to use; applications own their row data.
type List struct {
	Selected, Offset int
	mouseDown        bool
}

func (l *List) Clamp(count, visible int) {
	l.Selected = max(0, min(l.Selected, count-1))
	visible = max(1, visible)
	if l.Selected < l.Offset {
		l.Offset = l.Selected
	}
	if l.Selected >= l.Offset+visible {
		l.Offset = l.Selected - visible + 1
	}
	l.Offset = max(0, min(l.Offset, max(0, count-visible)))
}

// Handle returns handled,activate. Arrow/j/k/Home/End/Page keys and wheel only
// browse. Enter or a fresh left mouse press activate the selected row; held or
// release events never activate again. Applications decide what activation does.
func (l *List) Handle(event tcell.Event, count int, area Rect) (bool, bool) {
	switch event := event.(type) {
	case *tcell.EventKey:
		switch {
		case event.Key() == tcell.KeyUp || event.Rune() == 'k':
			l.Selected--
		case event.Key() == tcell.KeyDown || event.Rune() == 'j':
			l.Selected++
		case event.Key() == tcell.KeyHome:
			l.Selected = 0
		case event.Key() == tcell.KeyEnd:
			l.Selected = count - 1
		case event.Key() == tcell.KeyPgUp:
			l.Selected -= 10
		case event.Key() == tcell.KeyPgDn:
			l.Selected += 10
		case event.Key() == tcell.KeyEnter:
			l.Clamp(count, area.Height)
			return true, count > 0
		default:
			return false, false
		}
		l.Clamp(count, area.Height)
		return true, false
	case *tcell.EventMouse:
		buttons := event.Buttons()
		x, y := event.Position()
		inside := x >= area.X && x < area.X+area.Width && y >= area.Y && y < area.Y+area.Height
		if buttons&tcell.WheelUp != 0 {
			if !inside {
				return false, false
			}
			l.Selected--
			l.Clamp(count, area.Height)
			return true, false
		}
		if buttons&tcell.WheelDown != 0 {
			if !inside {
				return false, false
			}
			l.Selected++
			l.Clamp(count, area.Height)
			return true, false
		}
		pressed := buttons&tcell.Button1 != 0
		if !pressed {
			handled := l.mouseDown
			l.mouseDown = false
			return handled, false
		}
		if l.mouseDown {
			return true, false
		}
		if !inside {
			return false, false
		}
		index := l.Offset + y - area.Y
		if index < 0 || index >= count {
			return false, false
		}
		l.mouseDown = true
		l.Selected = index
		l.Clamp(count, area.Height)
		return true, true
	}
	return false, false
}

// Draw renders the visible rows using the canvas theme and returns the first
// unused screen row, so an app can place details beneath a short list.
func (l *List) Draw(canvas *Canvas, area Rect, count int, label func(int) string) int {
	l.Clamp(count, area.Height)
	y := area.Y
	for index := l.Offset; y < area.Y+max(0, area.Height) && index < count; index, y = index+1, y+1 {
		style := canvas.Theme.Normal
		if index == l.Selected {
			style = canvas.Theme.Selected
		}
		// Text also clips to the screen; clipping to the widget prevents one
		// list's wide rows overwriting a neighboring widget.
		canvas.textWidth(area.X, y, label(index), style, area.Width)
	}
	return y
}
