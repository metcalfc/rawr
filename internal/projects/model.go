// Package projects is the optional, deliberately saved workspace layer.
// The standalone pane apps have no dependency on this package.
package projects

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Schema = 2

type Recipe struct {
	Kind   string   `json:"kind"`
	Argv   []string `json:"argv"`
	Cwd    string   `json:"cwd"`
	Host   string   `json:"host"`
	Reopen string   `json:"reopen"`
}

type Node struct {
	Role      string  `json:"role,omitempty"`
	Direction string  `json:"direction,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
	Before    *Node   `json:"before,omitempty"`
	After     *Node   `json:"after,omitempty"`
}

type Tab struct {
	Name   string `json:"name"`
	Layout *Node  `json:"layout"`
}

type Project struct {
	Schema     int               `json:"schema"`
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Root       string            `json:"root"`
	Host       string            `json:"host"`
	Revision   int               `json:"revision"`
	Repository string            `json:"repository,omitempty"`
	VM         string            `json:"vm,omitempty"`
	Roles      map[string]Recipe `json:"roles"`
	Setup      []Tab             `json:"setup"`
}

type Result struct {
	Result        string   `json:"result"`
	ProjectID     string   `json:"project_id,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	Revision      int      `json:"revision"`
	Created       bool     `json:"created,omitempty"`
	LayoutMatches bool     `json:"layout_matches_saved"`
	Actions       []string `json:"actions,omitempty"`
	Conflicts     []string `json:"conflicts,omitempty"`
	Notices       []string `json:"notices,omitempty"`
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	v := hex.EncodeToString(value[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:], nil
}

func appKind(kind string) (string, string, error) {
	switch kind {
	case "files", "filerex", "filerawr":
		return "files", "filerawr", nil
	case "github", "ghrex", "ghrawr":
		return "github", "ghrawr", nil
	case "exedev", "exerex", "exerawr":
		return "exedev", "exerawr", nil
	default:
		return "", "", fmt.Errorf("unknown app %q; use filerawr, ghrawr or exerawr", kind)
	}
}

// Schema 2 layouts identify stable roles, independently of the app's executable
// name. Normalize old app recipes in memory; reads never rewrite saved setups.
func normalizeProject(p *Project) {
	for role, r := range p.Roles {
		if _, kind, err := appKind(r.Kind); err == nil {
			r.Kind = kind
			p.Roles[role] = r
		}
	}
}

func legacyAppName(kind string) string {
	switch kind {
	case "filerawr":
		return "filerex"
	case "ghrawr":
		return "ghrex"
	case "exerawr":
		return "exerex"
	}
	return ""
}

func leaf(role string) *Node { return &Node{Role: role} }
func split(direction string, ratio float64, before, after *Node) *Node {
	return &Node{Direction: direction, Ratio: ratio, Before: before, After: after}
}

func defaultSetup(roles map[string]Recipe) []Tab {
	n := leaf("work-main")
	_, gh := roles["github"]
	_, exe := roles["exedev"]
	if gh || exe {
		var right *Node
		if gh && exe {
			right = split("vertical", .5, leaf("github"), leaf("exedev"))
		} else if gh {
			right = leaf("github")
		} else {
			right = leaf("exedev")
		}
		n = split("horizontal", .7, n, right)
	}
	if _, ok := roles["files"]; ok {
		n = split("horizontal", .2, leaf("files"), n)
	}
	return []Tab{{Name: "Workspace", Layout: n}}
}

func Leaves(n *Node) []string {
	if n == nil {
		return nil
	}
	if n.Role != "" {
		return []string{n.Role}
	}
	return append(Leaves(n.Before), Leaves(n.After)...)
}

func Validate(p Project) error {
	if p.Schema != Schema {
		return fmt.Errorf("unsupported project schema %d (supports %d); explicitly import older definitions, or upgrade for newer files", p.Schema, Schema)
	}
	if len(p.ID) != 36 || p.ID[8] != '-' || p.ID[13] != '-' || p.ID[18] != '-' || p.ID[23] != '-' {
		return fmt.Errorf("invalid project ID")
	}
	if decoded, err := hex.DecodeString(strings.ReplaceAll(p.ID, "-", "")); err != nil || len(decoded) != 16 {
		return fmt.Errorf("invalid project ID")
	}
	if len(strings.TrimSpace(p.Name)) == 0 || len(p.Name) > 80 {
		return fmt.Errorf("project name must contain 1–80 bytes")
	}
	if !filepath.IsAbs(p.Root) || p.Host != "local" {
		return fmt.Errorf("project root must be an absolute local path")
	}
	if p.Revision < 0 {
		return fmt.Errorf("negative project revision")
	}
	if _, ok := p.Roles["work-main"]; !ok {
		return fmt.Errorf("work-main role is required")
	}
	appRoles := map[string]string{}
	for role, r := range p.Roles {
		if role == "" || len(role) > 60 || strings.ContainsAny(role, "\x00\r\n") {
			return fmt.Errorf("invalid role ID")
		}
		switch r.Kind {
		case "shell", "command", "vm-shell", "filerawr", "ghrawr", "exerawr":
		default:
			return fmt.Errorf("%s: unknown recipe kind %q", role, r.Kind)
		}
		if !filepath.IsAbs(r.Cwd) || r.Host != "local" {
			return fmt.Errorf("%s: recipe needs absolute local cwd", role)
		}
		if r.Reopen != "explicit" && r.Reopen != "shell-placeholder" {
			return fmt.Errorf("%s: invalid reopening policy", role)
		}
		if r.Kind == "shell" && (r.Reopen != "shell-placeholder" || len(r.Argv) > 0) {
			return fmt.Errorf("%s: shell placeholders cannot contain a relaunch command", role)
		}
		if r.Kind != "shell" && r.Reopen != "explicit" {
			return fmt.Errorf("%s: known program recipes must explicitly declare reopening", role)
		}
		if isApp(r.Kind) && len(r.Argv) > 0 {
			return fmt.Errorf("%s: built-in apps use the shared typed launch context", role)
		}
		if isApp(r.Kind) {
			if other, exists := appRoles[r.Kind]; exists {
				return fmt.Errorf("app %s has multiple role associations (%s and %s); inspect them before opening", r.Kind, other, role)
			}
			appRoles[r.Kind] = role
		}
		if (r.Kind == "command" || r.Kind == "vm-shell") && len(r.Argv) == 0 {
			return fmt.Errorf("%s: command recipe requires literal argv", role)
		}
		for _, arg := range r.Argv {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("%s: argv contains NUL", role)
			}
		}
	}
	seen := map[string]bool{}
	var check func(*Node, int) error
	check = func(n *Node, depth int) error {
		if n == nil || depth > 30 {
			return fmt.Errorf("invalid layout nesting")
		}
		if n.Role != "" {
			if n.Before != nil || n.After != nil || n.Direction != "" || n.Ratio != 0 {
				return fmt.Errorf("mixed role and split node")
			}
			if _, ok := p.Roles[n.Role]; !ok {
				return fmt.Errorf("layout references undeclared role %s", n.Role)
			}
			if seen[n.Role] {
				return fmt.Errorf("role %s appears twice", n.Role)
			}
			seen[n.Role] = true
			return nil
		}
		if (n.Direction != "horizontal" && n.Direction != "vertical") || !(n.Ratio >= .01 && n.Ratio <= .99) {
			return fmt.Errorf("invalid native split direction/ratio")
		}
		if err := check(n.Before, depth+1); err != nil {
			return err
		}
		return check(n.After, depth+1)
	}
	if len(p.Setup) == 0 {
		return fmt.Errorf("saved setup needs at least one tab")
	}
	for _, tab := range p.Setup {
		if err := check(tab.Layout, 0); err != nil {
			return err
		}
	}
	if !seen["work-main"] {
		return fmt.Errorf("saved setup must include work-main; associate the intended working pane before saving")
	}
	return nil
}

func newProject(name, root, repository, vm string, kinds []string) (Project, error) {
	resolved, err := filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return Project{}, fmt.Errorf("project root is inaccessible: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return Project{}, fmt.Errorf("project root is not an accessible directory: %s", resolved)
	}
	id, err := randomID()
	if err != nil {
		return Project{}, err
	}
	p := Project{Schema: Schema, ID: id, Name: name, Root: resolved, Host: "local", Repository: repository, VM: vm, Roles: map[string]Recipe{"work-main": {Kind: "shell", Argv: []string{}, Cwd: resolved, Host: "local", Reopen: "shell-placeholder"}}}
	for _, kind := range kinds {
		role, executable, err := appKind(kind)
		if err != nil {
			return Project{}, err
		}
		p.Roles[role] = Recipe{Kind: executable, Argv: []string{}, Cwd: resolved, Host: "local", Reopen: "explicit"}
	}
	p.Setup = defaultSetup(p.Roles)
	return p, Validate(p)
}
