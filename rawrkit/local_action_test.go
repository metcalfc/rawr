package rawrkit

import (
	"context"
	"errors"
	"testing"

	"github.com/metcalfc/rawr/rawrkit/rex"
)

func TestLocalActionRequiresIdleOriginalLocalWork(t *testing.T) {
	for _, condition := range []string{"idle", "login shell", "busy", "unknown", "different cwd", "alternate", "remote center"} {
		t.Run(condition, func(t *testing.T) {
			m, c, f := newFakeManager(t)
			shell := &rex.ProcessInfo{PID: 100, Path: "/bin/sh", CWD: c.Root}
			p := rex.Process{Child: shell, Foreground: shell}
			switch condition {
			case "login shell":
				p.Child = &rex.ProcessInfo{PID: 99, Path: "/usr/bin/login"}
				p.Ancestors = []rex.ProcessInfo{*p.Child}
			case "busy":
				p.Foreground = &rex.ProcessInfo{PID: 101, Path: "/usr/bin/vim", CWD: c.Root}
			case "unknown":
				p.Foreground = nil
			case "different cwd":
				shell.CWD = t.TempDir()
			case "alternate":
				f.screen = "alternate"
			case "remote center":
				if err := m.ActivateTerminal(context.Background(), c, TerminalSpec{ID: "remote", Title: "Remote", Argv: []string{"/bin/sleep", "60"}, Cwd: c.Root}); err != nil {
					t.Fatal(err)
				}
			}
			f.processes["main"] = p
			before := string(encode(f.viewSnapshot()))
			called := false
			err := m.RunLocalAction(context.Background(), c, func(context.Context) error { called = true; return nil })
			allowed := condition == "idle" || condition == "login shell"
			if called != allowed || (err == nil) != allowed {
				t.Fatalf("called=%v err=%v", called, err)
			}
			if !allowed && before != string(encode(f.viewSnapshot())) {
				t.Fatal("refused action changed live work")
			}
			f.assertNoDestructiveCalls(t)
		})
	}
}

func TestLocalActionFailureDoesNotRetryOrMoveWork(t *testing.T) {
	m, c, f := newFakeManager(t)
	shell := &rex.ProcessInfo{PID: 100, Path: "/bin/sh", CWD: c.Root}
	f.processes["main"] = rex.Process{Child: shell, Foreground: shell}
	want := errors.New("dirty checkout")
	calls := 0
	err := m.RunLocalAction(context.Background(), c, func(context.Context) error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 || f.count("session.focus_block") != 0 {
		t.Fatalf("%v, calls %d", err, calls)
	}
	f.assertNoDestructiveCalls(t)
}
