package rex

import "encoding/json"

// TestedVersion is the Rex build rawrkit was last verified against. It is
// informational: Rex is in beta and ships often, so the gate is MinAPIRevision
// plus the capabilities rawrkit needs (see RequiredMethods), not this string.
const TestedVersion = "0.1.0-preview.18"

// MinAPIRevision is the oldest Rex API revision whose schemas rawrkit's calls
// were verified against. Newer revisions are accepted when they still list
// every required capability; a renamed parameter fails loudly because Rex
// rejects unknown input properties.
const MinAPIRevision = 13

// RequiredMethods are the server API methods rawrkit calls.
var RequiredMethods = []string{
	"client.list", "client.perform",
	"session.create", "session.detach_block", "session.focus_block", "session.list",
	"session.list_blocks", "session.move_block", "session.new_split", "session.new_window",
	"session.set_block_label", "session.set_window_label", "session.swap_blocks", "session.view",
}

// RequiredTerminalMethods are the terminal block methods rawrkit calls.
var RequiredTerminalMethods = []string{"modes", "process"}

// TerminalShellFlavor is the block flavor rawrkit creates.
const TerminalShellFlavor = TerminalCreator + ".shell"

type Identity struct {
	HostID      string `json:"host_id"`
	InstanceID  string `json:"instance_id"`
	Version     string `json:"rex_version"`
	APIRevision int    `json:"api_revision"`
	Endpoint    string `json:"endpoint"`
	SessionID   string `json:"session_id,omitempty"`
	BlockID     string `json:"block_id,omitempty"`
}
type Session struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}
type View struct {
	ID             string   `json:"session_id"`
	Label          string   `json:"label"`
	Revision       int      `json:"revision"`
	ActiveWindowID string   `json:"active_window_id"`
	Windows        []Window `json:"windows"`
}
type Window struct {
	ID             string  `json:"window_id"`
	Label          string  `json:"label"`
	FocusedBlockID string  `json:"focused_block_id"`
	Layers         []Layer `json:"layers"`
}
type Layer struct {
	ID     string      `json:"layer_id"`
	Kind   string      `json:"kind"`
	Bounds []float64   `json:"bounds"`
	Layout *LayoutNode `json:"layout"`
	Blocks []Block     `json:"blocks"`
	State  string      `json:"state,omitempty"`
}
type LayoutNode struct {
	BlockID string    `json:"block_id,omitempty"`
	Rect    []float64 `json:"rect,omitempty"`
	Split   *Split    `json:"split,omitempty"`
}
type Split struct {
	ID        string      `json:"split_id,omitempty"`
	Direction string      `json:"direction"`
	Ratio     float64     `json:"ratio"`
	Before    *LayoutNode `json:"before"`
	After     *LayoutNode `json:"after"`
}
type Block struct {
	ID        string `json:"block_id"`
	Label     string `json:"label"`
	HostID    string `json:"host_id"`
	Creator   string `json:"creator_name"`
	Flavor    string `json:"flavor"`
	Placement string `json:"placement,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
}
type ProcessInfo struct {
	PID         int    `json:"pid"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	InvokedPath string `json:"invoked_path,omitempty"`
	Argv0       string `json:"argv0,omitempty"`
	Script      string `json:"script,omitempty"`
	CWD         string `json:"cwd,omitempty"`
}
type Process struct {
	Child      *ProcessInfo    `json:"child"`
	Foreground *ProcessInfo    `json:"foreground"`
	Ancestors  []ProcessInfo   `json:"ancestors"`
	LastExit   json.RawMessage `json:"last_exit"`
}
type Created struct {
	SessionID      string    `json:"session_id"`
	WindowID       string    `json:"window_id"`
	BlockIDs       []string  `json:"block_ids"`
	InitialWindows []Created `json:"initial_windows"`
	Revision       int       `json:"revision"`
}

func (v View) Blocks() map[string]Block {
	result := map[string]Block{}
	for _, w := range v.Windows {
		for _, l := range w.Layers {
			for _, b := range l.Blocks {
				b.WindowID = w.ID
				result[b.ID] = b
			}
		}
	}
	return result
}
func (v View) Focused() string {
	for _, w := range v.Windows {
		if w.ID == v.ActiveWindowID {
			return w.FocusedBlockID
		}
	}
	return ""
}
func Leaves(n *LayoutNode) []string {
	if n == nil {
		return nil
	}
	if n.BlockID != "" {
		return []string{n.BlockID}
	}
	if n.Split == nil {
		return nil
	}
	return append(Leaves(n.Split.Before), Leaves(n.Split.After)...)
}
