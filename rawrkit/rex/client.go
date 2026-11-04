package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Runner func(context.Context, string, []string) ([]byte, []byte, error)
type Client struct {
	Executable string
	Server     string
	ClientID   string
	Runner     Runner
	mu         sync.Mutex
	instance   string
	revisions  map[string]int
}

func New(executable, server, clientID string) *Client {
	if executable == "" {
		executable = os.Getenv("RAWR_REX")
	}
	if executable == "" {
		executable = os.Getenv("REXAPPS_REX")
	}
	if executable == "" {
		executable, _ = exec.LookPath("rex")
	}
	if executable == "" && runtime.GOOS == "darwin" {
		executable = "/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex"
	}
	if executable == "" {
		executable = "rex"
	}
	if server == "" {
		server = os.Getenv("RAWR_SERVER")
	}
	return &Client{Executable: executable, Server: server, ClientID: clientID, revisions: map[string]int{}}
}
func defaultRun(ctx context.Context, exe string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}
func (c *Client) Run(ctx context.Context, args []string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	base := []string{"--autostart=false", "--timeout", "8s"}
	if c.Server != "" {
		base = append(base, "-S", c.Server)
	}
	run := c.Runner
	if run == nil {
		run = defaultRun
	}
	stdout, stderr, err := run(ctx, c.Executable, append(base, args...))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("Rex timed out; creation may have succeeded: inspect state before retrying")
		}
		detail := strings.TrimSpace(string(stderr))
		if len(detail) > 1600 {
			detail = detail[:1600]
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("Rex: %s", detail)
	}
	if out == nil {
		return nil
	}
	if !json.Valid(stdout) {
		return fmt.Errorf("malformed Rex JSON; no retry performed")
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("malformed Rex result: %w", err)
	}
	return nil
}
func (c *Client) Call(ctx context.Context, method string, args any, out any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return c.Run(ctx, []string{"api", "call", method, string(data)}, out)
}
func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var i Identity
	if err := c.Run(ctx, []string{"whoami", "--json"}, &i); err != nil {
		return i, err
	}
	if i.Version != TestedVersion {
		return i, fmt.Errorf("unsupported Rex version %q; tested %s; repeat capability discovery before mutation", i.Version, TestedVersion)
	}
	if i.HostID == "" || i.InstanceID == "" || !strings.HasPrefix(i.Endpoint, "unix:") {
		return i, fmt.Errorf("Rex requires a verified local Unix endpoint and host/generation identity; remote Rex is not supported")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.instance != i.InstanceID {
		c.instance = i.InstanceID
		c.revisions = map[string]int{}
	}
	return i, nil
}
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var result struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.Call(ctx, "session.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	if result.Sessions == nil {
		return nil, fmt.Errorf("malformed Rex session list")
	}
	for _, s := range result.Sessions {
		if s.ID == "" {
			return nil, fmt.Errorf("malformed Rex session identity")
		}
	}
	return result.Sessions, nil
}
func (c *Client) View(ctx context.Context, sid string) (View, error) {
	var raw json.RawMessage
	var v View
	if err := c.Call(ctx, "session.view", map[string]any{"session_id": sid}, &raw); err != nil {
		return v, err
	}
	var required struct {
		Revision *int            `json:"revision"`
		Windows  json.RawMessage `json:"windows"`
	}
	if err := json.Unmarshal(raw, &required); err != nil || required.Revision == nil || len(required.Windows) == 0 || bytes.Equal(bytes.TrimSpace(required.Windows), []byte("null")) {
		return v, fmt.Errorf("malformed Rex view: revision and windows are required")
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, err
	}
	if v.ID != sid || v.Revision < 0 {
		return v, fmt.Errorf("malformed/mismatched Rex view")
	}
	seen := map[string]bool{}
	for _, w := range v.Windows {
		if w.ID == "" {
			return v, fmt.Errorf("malformed Rex window")
		}
		for _, l := range w.Layers {
			if err := ValidateLayout(l.Layout, 0); err != nil {
				return v, err
			}
			for _, b := range l.Blocks {
				if b.ID == "" || seen[b.ID] {
					return v, fmt.Errorf("malformed/duplicate Rex block identity")
				}
				seen[b.ID] = true
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revisions == nil {
		c.revisions = map[string]int{}
	}
	if old, ok := c.revisions[sid]; ok && v.Revision < old {
		return v, fmt.Errorf("stale Rex view revision; refusing out-of-order observation")
	}
	c.revisions[sid] = v.Revision
	return v, nil
}
func ValidateLayout(n *LayoutNode, depth int) error {
	if n == nil {
		return nil
	}
	if depth > 40 {
		return fmt.Errorf("Rex layout is too deeply nested")
	}
	if n.BlockID != "" {
		if n.Split != nil {
			return fmt.Errorf("ambiguous Rex layout node")
		}
		return nil
	}
	if n.Split == nil {
		return fmt.Errorf("malformed Rex layout node")
	}
	s := n.Split
	if (s.Direction != "horizontal" && s.Direction != "vertical") || s.Ratio <= 0 || s.Ratio >= 1 || s.Before == nil || s.After == nil {
		return fmt.Errorf("malformed Rex split")
	}
	if err := ValidateLayout(s.Before, depth+1); err != nil {
		return err
	}
	return ValidateLayout(s.After, depth+1)
}
func (c *Client) Inventory(ctx context.Context, sid string) ([]Block, error) {
	var result struct {
		Blocks []Block `json:"blocks"`
	}
	if err := c.Call(ctx, "session.list_blocks", map[string]any{"session_id": sid}, &result); err != nil {
		return nil, err
	}
	if result.Blocks == nil {
		return nil, fmt.Errorf("malformed Rex block inventory")
	}
	seen := map[string]bool{}
	for _, b := range result.Blocks {
		if b.ID == "" || seen[b.ID] {
			return nil, fmt.Errorf("malformed Rex inventory identity")
		}
		seen[b.ID] = true
	}
	return result.Blocks, nil
}
func (c *Client) Process(ctx context.Context, sid, bid string) (Process, error) {
	var p Process
	err := c.Run(ctx, []string{"block", "call", "-s", sid, "-b", bid, "process"}, &p)
	return p, err
}
func (c *Client) Select(ctx context.Context, sid, bid string) error {
	var result struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info struct {
				Kind  string `json:"kind"`
				Label string `json:"label"`
			} `json:"info"`
		} `json:"clients"`
	}
	if err := c.Call(ctx, "client.list", map[string]any{}, &result); err != nil {
		return err
	}
	client := ""
	count := 0
	for _, v := range result.Clients {
		if v.Info.Kind == "app" && (c.ClientID == "" || c.ClientID == v.ID || c.ClientID == v.Info.Label) {
			client = v.ID
			count++
		}
	}
	if count > 1 {
		return fmt.Errorf("multiple Rex apps connected; choose --client")
	}
	if client != "" {
		if err := c.Run(ctx, []string{"-C", client, "do", "session.select", "session_id=" + sid, "--json"}, nil); err != nil {
			return err
		}
	}
	if bid != "" {
		return c.Call(ctx, "session.focus_block", map[string]any{"session_id": sid, "block_id": bid}, nil)
	}
	return nil
}
// Leaf retains a work terminal's surface after its program exits.
func Leaf(label string, argv []string, cwd string) map[string]any {
	return terminalLeaf(label, argv, cwd, false)
}

// AppLeaf lets Rex close a dedicated resource pane when its program exits.
// Rex's default quick-failure threshold remains in effect to retain startup errors.
func AppLeaf(label string, argv []string, cwd string) map[string]any {
	return terminalLeaf(label, argv, cwd, true)
}

func terminalLeaf(label string, argv []string, cwd string, closeOnExit bool) map[string]any {
	options := map[string]any{"cwd": cwd, "exit": map[string]any{"on_completion": closeOnExit}}
	if len(argv) > 0 {
		options["command"] = argv
		options["shell"] = "none"
		options["login"] = false
	}
	var deps []string
	seen := map[string]bool{}
	for _, name := range []string{"gh", "git", "ssh", "open", "xdg-open"} {
		if exe, err := exec.LookPath(name); err == nil {
			dir := filepath.Dir(exe)
			if !seen[dir] {
				deps = append(deps, dir)
				seen[dir] = true
			}
		}
	}
	if len(deps) > 0 {
		options["path_prepend"] = deps
	}
	return map[string]any{"block": map[string]any{"flavor": "com.superlogical.terminal.shell", "label": label, "options": options}}
}
