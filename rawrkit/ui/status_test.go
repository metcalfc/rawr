package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestEncodeStatusFollowsOSC7501(t *testing.T) {
	for _, test := range []struct {
		status ProgramStatus
		want   string
	}{
		{ProgramStatus{App: "ghrawr", State: "idle"}, "\x1b]7501;state=idle:app=ghrawr\x1b\\"},
		{ProgramStatus{App: "ghrawr", State: "blocked", Kind: "permission", Message: "Check out PR #42? y/n"}, "\x1b]7501;state=blocked:kind=permission:app=ghrawr:msg=" + base64.StdEncoding.EncodeToString([]byte("Check out PR #42? y/n")) + "\x1b\\"},
		// kind only applies to blocked; an invalid app is left out, not sent.
		{ProgramStatus{App: "bad app:name", State: "working", Kind: "permission"}, "\x1b]7501;state=working\x1b\\"},
		{ProgramStatus{State: "clear"}, ""},
		{ProgramStatus{State: "busy"}, ""},
	} {
		if got := encodeStatus(test.status); got != test.want {
			t.Errorf("%+v: got %q want %q", test.status, got, test.want)
		}
	}
	got := encodeStatus(ProgramStatus{State: "working", Message: "line\none\x1b[31m"})
	encoded := got[strings.Index(got, "msg=")+4 : len(got)-2]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || bytes.ContainsAny(decoded, "\n\x1b") {
		t.Fatalf("control characters reached msg: %q %v", decoded, err)
	}
}

type statusTTY struct {
	tcell.Tty
	out bytes.Buffer
}

func (t *statusTTY) Write(p []byte) (int, error) { return t.out.Write(p) }

type ttyScreen struct {
	tcell.SimulationScreen
	tty *statusTTY
}

func (s ttyScreen) Tty() (tcell.Tty, bool) { return s.tty, true }

type statusView struct{ frames int }

func (v *statusView) Init(*Canvas) error                       { return nil }
func (v *statusView) Refresh(context.Context)                  {}
func (v *statusView) Draw(*Canvas)                             { v.frames++ }
func (v *statusView) Handle(context.Context, tcell.Event) bool { return v.frames >= 3 }
func (v *statusView) ProgramStatus() ProgramStatus {
	if v.frames == 1 {
		return ProgramStatus{App: "test", State: "working"}
	}
	return ProgramStatus{App: "test", State: "idle"}
}

func TestRunScreenReportsChangesAndClearsOnExit(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	screen := ttyScreen{sim, &statusTTY{}}
	view := &statusView{}
	for i := 0; i < 3; i++ {
		sim.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	}
	if err := RunScreen(context.Background(), screen, view); err != nil {
		t.Fatal(err)
	}
	want := "\x1b]7501;state=working:app=test\x1b\\" + "\x1b]7501;state=idle:app=test\x1b\\" + clearStatus
	if got := screen.tty.out.String(); got != want {
		t.Fatalf("reports %q, want %q", got, want)
	}
}
