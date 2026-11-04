package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/storage"
	panes "github.com/metcalfc/rawr/rawrkit"
)

func makeContext(ctx context.Context, o options) (model.Context, error) {
	c := model.Context{}
	if o.contextFile != "" {
		if err := storage.ReadJSON(o.contextFile, &c); err != nil {
			return c, fmt.Errorf("read explicit app context: %w", err)
		}
	}
	set := func(flag, value string, field *string) {
		if o.visited[flag] {
			*field = value
		}
	}
	set("root", o.root, &c.Root)
	set("repo", o.repository, &c.Repository)
	set("scope", o.scope, &c.Scope)
	set("session", o.session, &c.Session)
	set("anchor", o.anchor, &c.Anchor)
	set("rex", o.rex, &c.Rex)
	set("server", o.server, &c.Server)
	set("client", o.client, &c.Client)
	set("state-dir", o.stateDir, &c.StateDir)
	set("vm", o.vm, &c.VM)
	root, err := resolveRoot(ctx, c.Root)
	if err != nil {
		return c, err
	}
	c.Root = root
	if c.Name == "" {
		c.Name = filepath.Base(root)
	}
	if c.Scope == "" {
		c.Scope = panes.ScopeForRoot(root)
	}
	if len(c.Scope) > 128 || strings.ContainsAny(c.Scope, "\x00\r\n") {
		return c, fmt.Errorf("scope must be 1–128 bytes without control characters")
	}
	inheritedSession := os.Getenv("REX_SESSION")
	if c.Session == "" && !o.visited["session"] {
		c.Session = inheritedSession
	}
	if c.Anchor == "" && !o.visited["anchor"] && c.Session == inheritedSession {
		c.Anchor = os.Getenv("REX_BLOCK")
	}
	if c.Client == "" {
		c.Client = os.Getenv("REX_CLIENT")
	}
	if c.StateDir == "" {
		c.StateDir, err = defaultDir("state", "rexapps")
		if err != nil {
			return c, err
		}
	}
	c.StateDir, err = filepath.Abs(c.StateDir)
	return c, err
}

func resolveRoot(ctx context.Context, value string) (string, error) {
	if value == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		value = cwd
		// This read only discovers the local checkout. It never follows focus/SSH.
		gitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(gitCtx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
			if found := strings.TrimSpace(string(output)); found != "" {
				value = found
			}
		}
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("project root is inaccessible: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project root must be an accessible directory: %s", root)
	}
	return root, nil
}

func defaultDir(kind, namespace string) (string, error) {
	key := "XDG_CONFIG_HOME"
	suffix := ".config"
	if kind == "state" {
		key, suffix = "XDG_STATE_HOME", filepath.Join(".local", "state")
	}
	base := os.Getenv(key)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, suffix)
	}
	return filepath.Abs(filepath.Join(base, namespace))
}
