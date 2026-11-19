package ui

import (
	"encoding/base64"
	"io"
	"regexp"
	"strings"
)

// ProgramStatus is the app's current state in the Program Status Protocol
// (OSC 7501). Rex shows it as tab spinners and blocked/finished indicators, and
// scripts receive it as terminal.program_status_changed events. Terminals that
// do not support the protocol ignore the sequence.
//
// State is idle, working, done, blocked or error. Kind (permission, question
// or auth) applies only to blocked. Message is one short human-readable line.
type ProgramStatus struct {
	App     string
	State   string
	Kind    string
	Message string
}

// StatusReporter is an optional View extension. RunScreen asks for the status
// after every frame and reports it only when it changes; on exit it clears it.
type StatusReporter interface {
	ProgramStatus() ProgramStatus
}

var (
	statusApp    = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)
	statusStates = map[string]bool{"idle": true, "working": true, "done": true, "blocked": true, "error": true}
	statusKinds  = map[string]bool{"permission": true, "question": true, "auth": true}
)

// encodeStatus returns the OSC 7501 report for s, or "" if s has no valid state.
func encodeStatus(s ProgramStatus) string {
	if !statusStates[s.State] {
		return ""
	}
	pairs := []string{"state=" + s.State}
	if s.State == "blocked" && statusKinds[s.Kind] {
		pairs = append(pairs, "kind="+s.Kind)
	}
	// app is repeated on every report because each report replaces its record.
	if statusApp.MatchString(s.App) {
		pairs = append(pairs, "app="+s.App)
	}
	if msg := statusText(s.Message); msg != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(msg)))
	}
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\"
}

// statusText keeps the decoded message free of control characters, which
// would make the terminal discard the whole report.
func statusText(value string) string {
	value = strings.Join(strings.Fields(SafeText(value)), " ")
	if len(value) > 200 {
		value = strings.ToValidUTF8(value[:200], "")
	}
	return value
}

const clearStatus = "\x1b]7501;state=clear\x1b\\"

// statusReporter writes reports for one screen, skipping repeats.
type statusReporter struct {
	out  io.Writer
	last string
	sent bool
}

func (r *statusReporter) report(s ProgramStatus) {
	if r == nil || r.out == nil {
		return
	}
	seq := encodeStatus(s)
	if seq == "" || seq == r.last {
		return
	}
	if _, err := io.WriteString(r.out, seq); err == nil {
		r.last, r.sent = seq, true
	}
}
func (r *statusReporter) clear() {
	if r != nil && r.out != nil && r.sent {
		_, _ = io.WriteString(r.out, clearStatus)
		r.last, r.sent = "", false
	}
}
