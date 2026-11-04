package ui

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// ResourceStatus remembers the last successful refresh. A failed request does
// not make the last good data look current; the application retains its rows.
type ResourceStatus struct {
	Updated time.Time
	Error   string
}

func (s *ResourceStatus) Success() { s.Updated, s.Error = time.Now(), "" }
func (s *ResourceStatus) Fail(err error) {
	if err != nil {
		s.Error = err.Error()
	}
}
func (s ResourceStatus) Line(loading bool) string {
	state := "Not refreshed"
	if !s.Updated.IsZero() {
		state = "Updated " + s.Updated.Format("15:04:05")
	}
	if s.Error != "" {
		state = "Refresh failed"
		if !s.Updated.IsZero() {
			state = "Stale · last updated " + s.Updated.Format("15:04:05")
		}
	}
	if loading {
		state = "Refreshing · " + state
	}
	return state
}

// Document is a scrollable read-only text view for help and resource details.
// It handles navigation only; applications decide how to leave or act on it.
type Document struct{ Offset int }

// WrapLines fits help and detail text to a narrow pane. Source/document viewers
// can retain their original lines instead of opting into wrapping.
func WrapLines(lines []string, width int) []string {
	width = max(1, width)
	var out []string
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		var part strings.Builder
		cells := 0
		for _, r := range SafeText(line) {
			n := uniseg.StringWidth(string(r))
			if cells+n > width && cells > 0 {
				out = append(out, part.String())
				part.Reset()
				cells = 0
			}
			part.WriteRune(r)
			cells += n
		}
		if part.Len() > 0 {
			out = append(out, part.String())
		}
	}
	return out
}

func (d *Document) Handle(event tcell.Event, count, height int) bool {
	step := max(1, height-1)
	switch e := event.(type) {
	case *tcell.EventKey:
		switch {
		case e.Key() == tcell.KeyUp || e.Rune() == 'k':
			d.Offset--
		case e.Key() == tcell.KeyDown || e.Rune() == 'j':
			d.Offset++
		case e.Key() == tcell.KeyPgUp:
			d.Offset -= step
		case e.Key() == tcell.KeyPgDn:
			d.Offset += step
		case e.Key() == tcell.KeyHome:
			d.Offset = 0
		case e.Key() == tcell.KeyEnd:
			d.Offset = count - 1
		default:
			return false
		}
	case *tcell.EventMouse:
		switch {
		case e.Buttons()&tcell.WheelUp != 0:
			d.Offset--
		case e.Buttons()&tcell.WheelDown != 0:
			d.Offset++
		default:
			return false
		}
	default:
		return false
	}
	d.Offset = max(0, min(d.Offset, max(0, count-max(1, height))))
	return true
}

func (d *Document) Draw(canvas *Canvas, area Rect, lines []string) {
	d.Offset = max(0, min(d.Offset, max(0, len(lines)-max(1, area.Height))))
	for y, i := area.Y, d.Offset; y < area.Y+area.Height && i < len(lines); y, i = y+1, i+1 {
		canvas.textWidth(area.X, y, lines[i], canvas.Theme.Normal, area.Width)
	}
}
