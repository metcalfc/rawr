package command

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/projects"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

func runRawr(ctx context.Context, o options, args []string, out io.Writer) (int, error) {
	if o.tui || o.toggle || o.hide || o.status || o.local || o.contextFile != "" {
		return 0, fmt.Errorf("use Rawr subcommands for projects, or ghrawr/filerawr/exerawr for app flags")
	}
	configDir, stateDir := o.configDir, o.stateDir
	var err error
	if configDir == "" {
		configDir, err = defaultDir("config", "rawr-go")
		if err != nil {
			return 0, err
		}
	}
	if stateDir == "" {
		stateDir, err = defaultDir("state", "rawr-go")
		if err != nil {
			return 0, err
		}
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return 0, err
	}
	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return 0, err
	}
	client := rex.New(o.rex, o.server, o.client)
	manager := projects.New(client, configDir, stateDir, binaryDir())
	appsDir := o.appsStateDir
	if appsDir == "" {
		appsDir, err = defaultDir("state", "rexapps")
		if err != nil {
			return 0, err
		}
	}
	manager.AppsDir, err = filepath.Abs(appsDir)
	if err != nil {
		return 0, err
	}
	command, rest := args[0], args[1:]
	require := func(count int) error {
		if len(rest) != count {
			return fmt.Errorf("%s expects %d argument(s); use rawr --help", command, count)
		}
		return nil
	}
	var value any
	switch command {
	case "doctor":
		if err = require(0); err != nil {
			break
		}
		identity, identityErr := client.Identity(ctx)
		dependencies := map[string]string{}
		for _, name := range []string{"ghrawr", "filerawr", "exerawr", "git", "gh", "ssh"} {
			path := name
			if name == "ghrawr" || name == "filerawr" || name == "exerawr" {
				path = filepath.Join(binaryDir(), name)
			}
			if found, findErr := exec.LookPath(path); findErr == nil {
				dependencies[name] = found
			} else {
				dependencies[name] = "missing"
			}
		}
		value = map[string]any{"rex": identity, "dependencies": dependencies, "config_dir": configDir, "state_dir": stateDir, "apps_state_dir": manager.AppsDir, "verified_runtime": "macOS; other platforms cross-compiled only", "program_persistence": "Rex restart retained terminal surfaces with fresh shells; running programs and transcripts are not restored"}
		if identityErr != nil {
			value.(map[string]any)["rex_error"] = identityErr.Error()
			if err = printResult(out, value, nil); err != nil {
				return 0, err
			}
			return 1, nil
		}
	case "create":
		if len(rest) < 1 || len(rest) > 2 {
			err = fmt.Errorf("create expects NAME [ROOT]; use --root PATH or a second positional root")
			break
		}
		root := o.root
		if len(rest) == 2 {
			if o.visited["root"] {
				err = fmt.Errorf("choose a positional root or --root")
				break
			}
			root = rest[1]
		}
		root, err = resolveRoot(ctx, root)
		if err != nil {
			break
		}
		var kinds []string
		if strings.TrimSpace(o.appList) != "" {
			for _, kind := range strings.Split(o.appList, ",") {
				kinds = append(kinds, strings.TrimSpace(kind))
			}
		}
		value, err = manager.Create(rest[0], root, o.repository, o.vm, kinds)
	case "list":
		if err = require(0); err == nil {
			value, err = manager.List()
		}
	case "show":
		if err = require(1); err == nil {
			value, err = manager.Get(rest[0])
		}
	case "link":
		if err = require(2); err != nil {
			break
		}
		vm, findErr := services.FindVM(ctx, rest[1])
		if findErr != nil {
			err = findErr
			break
		}
		value, err = manager.Link(ctx, rest[0], vm)
	case "open", "save", "plan", "restore", "status":
		if err = require(1); err != nil {
			break
		}
		var result projects.Result
		switch command {
		case "open":
			result, err = manager.Open(ctx, rest[0])
		case "save":
			var expected *int
			if o.expectedRevision >= 0 {
				expected = &o.expectedRevision
			}
			result, err = manager.Save(ctx, rest[0], expected)
		case "plan", "status":
			result, err = manager.Plan(ctx, rest[0])
		case "restore":
			result, err = manager.Restore(ctx, rest[0])
		}
		if err == nil {
			if err = printResult(out, result, nil); err != nil {
				return 0, err
			}
			if result.Result == "partial" || result.Result == "failed" {
				return 2, nil
			}
			return 0, nil
		}
	case "open-app", "toggle", "hide-app", "term":
		count := 2
		if command == "term" {
			count = 1
		}
		if err = require(count); err != nil {
			break
		}
		c, contextErr := manager.Context(ctx, rest[0])
		if contextErr != nil {
			err = contextErr
			break
		}
		appManager := catalog.NewManager(client, c.StateDir, binaryDir())
		if command == "term" {
			value, err = appManager.LocalTerm(ctx, c)
			break
		}
		kind, kindErr := appName(rest[1])
		if kindErr != nil {
			err = kindErr
			break
		}
		switch command {
		case "open-app":
			value, err = appManager.Open(ctx, c, kind)
		case "toggle":
			value, err = appManager.Toggle(ctx, c, kind)
		case "hide-app":
			value, err = appManager.Hide(ctx, c, kind)
		}
	default:
		err = fmt.Errorf("unknown command %q; use rawr --help", command)
	}
	return 0, printResult(out, value, err)
}

func appName(value string) (string, error) {
	value = catalog.Canonical(value)
	switch value {
	case "files", "filerawr":
		return "filerawr", nil
	case "github", "ghrawr":
		return "ghrawr", nil
	case "exedev", "exerawr":
		return "exerawr", nil
	default:
		return "", fmt.Errorf("unknown app %q; use filerawr, ghrawr or exerawr", value)
	}
}
