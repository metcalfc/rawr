// Package command provides the four small Go entry points. Standalone apps
// depend on shared pane behavior, without requiring a Rawr project definition.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/metcalfc/rawr/internal/apps"
	"github.com/metcalfc/rawr/internal/buildinfo"
	"github.com/metcalfc/rawr/internal/catalog"
	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	panes "github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/rex"
)

type options struct {
	root, repository, scope, session, anchor, rex, server, client string
	stateDir, configDir, appsStateDir, vm, contextFile, appList   string
	viewFile                                                      string
	diff, staged                                                  bool
	tui, toggle, hide, status, local, version, help               bool
	verbose                                                       bool
	expectedRevision                                              int
	visited                                                       map[string]bool
}

func Main(name string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(Run(ctx, name, os.Args[1:], os.Stdout, os.Stderr))
}

// Run returns a shell exit code: 0 complete, 1 error, 2 partial saved restore.
func Run(ctx context.Context, name string, args []string, out, errOut io.Writer) int {
	name = catalog.Canonical(name)
	o, positional, err := parse(name, args, errOut)
	if err != nil {
		fmt.Fprintln(errOut, name+":", err)
		return 1
	}
	if o.version {
		fmt.Fprintf(out, "%s %s (%s) %s\n", name, buildinfo.Version, buildinfo.Commit, rex.TestedVersion)
		return 0
	}
	if o.help || name == "rawr" && len(positional) == 0 {
		usage(out, name)
		return 0
	}
	if name == "rawr" {
		code, err := runRawr(ctx, o, positional, out)
		if err != nil {
			fmt.Fprintln(errOut, "rawr:", err)
			return 1
		}
		return code
	}
	if len(positional) != 0 {
		fmt.Fprintln(errOut, name+": unexpected arguments; use --root PATH or --help")
		return 1
	}
	if name != "ghrawr" && name != "filerawr" && name != "exerawr" {
		fmt.Fprintln(errOut, "unknown app:", name)
		return 1
	}
	if o.local && name != "exerawr" {
		fmt.Fprintln(errOut, name+": --local is available through exerawr")
		return 1
	}
	c, err := makeContext(ctx, o)
	if err == nil {
		if o.viewFile != "" {
			err = apps.RunFileView(ctx, c.Root, o.viewFile, o.diff, o.staged)
		} else {
			err = runApp(ctx, name, o, c, out)
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, name+":", err)
		return 1
	}
	return 0
}

func runApp(ctx context.Context, name string, o options, c model.Context, out io.Writer) error {
	if !o.verbose && !o.status {
		out = io.Discard
	}
	client := rex.New(c.Rex, c.Server, c.Client)
	manager := catalog.NewManager(client, c.StateDir, binaryDir())
	if o.local {
		value, err := manager.LocalTerm(ctx, c)
		return printResult(out, value, err)
	}
	if o.tui {
		callbacks := model.Callbacks{}
		callbacks.CheckoutPR = func(pr model.PR) error {
			action := func(actionCtx context.Context) error { return services.CheckoutPR(actionCtx, c.Root, c.Repository, pr) }
			if c.Session != "" {
				return manager.RunLocalAction(ctx, c, action)
			}
			return action(ctx)
		}
		if c.Session != "" {
			callbacks.OpenFile = func(request model.FileRequest) error {
				spec, err := catalog.FileTerminal(binaryDir(), c.Root, request)
				if err != nil {
					return err
				}
				return manager.ActivateTerminal(ctx, c, spec)
			}
			callbacks.SwitchVM = func(vm model.VM) error {
				return func() error {
					spec, err := catalog.Terminal(vm, c.Root)
					if err != nil {
						return err
					}
					return manager.ActivateTerminal(ctx, c, spec)
				}()
			}
		}
		return apps.Run(ctx, name, c, callbacks)
	}
	if o.status {
		value, err := manager.Status(ctx, c)
		return printResult(out, value, err)
	}
	var value panes.Result
	var err error
	switch {
	case o.toggle:
		value, err = manager.Toggle(ctx, c, name)
	case o.hide:
		value, err = manager.Hide(ctx, c, name)
	default:
		value, err = manager.Open(ctx, c, name)
	}
	return printResult(out, value, err)
}

func parse(name string, args []string, errOut io.Writer) (options, []string, error) {
	var o options
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.root, "root", "", "anchored project directory (default Git root, otherwise cwd)")
	f.StringVar(&o.repository, "repo", "", "explicit GitHub OWNER/NAME")
	f.StringVar(&o.scope, "scope", "", "stable app scope (default derived from canonical root)")
	f.StringVar(&o.session, "session", "", "full Rex session ID (default REX_SESSION)")
	f.StringVar(&o.anchor, "anchor", "", "working pane ID (default REX_BLOCK in the same session)")
	f.StringVar(&o.rex, "rex", "", "Rex CLI path")
	f.StringVar(&o.server, "server", "", "explicit local Rex server address")
	f.StringVar(&o.client, "client", "", "Rex app client ID or label")
	f.StringVar(&o.stateDir, "state-dir", "", "runtime state directory")
	f.StringVar(&o.configDir, "config-dir", "", "Rawr saved project directory")
	f.StringVar(&o.appsStateDir, "apps-state-dir", "", "Rawr shared app state directory (default rexapps)")
	f.StringVar(&o.vm, "vm", "", "linked existing VM name")
	f.StringVar(&o.contextFile, "context-file", "", "explicit native launch context JSON")
	f.StringVar(&o.appList, "apps", "filerawr,ghrawr,exerawr", "Rawr initial app panes (comma separated; empty is shell only)")
	f.StringVar(&o.viewFile, "view-file", "", "filerawr: read-only file or diff viewer in this terminal")
	f.BoolVar(&o.diff, "diff", false, "filerawr --view-file: show Git diff")
	f.BoolVar(&o.staged, "staged", false, "filerawr --view-file --diff: show staged diff")
	f.IntVar(&o.expectedRevision, "expected-revision", -1, "require this saved revision before saving")
	f.BoolVar(&o.tui, "tui", false, "run UI in this terminal without opening another pane")
	f.BoolVar(&o.toggle, "toggle", false, "hide/show this native app pane")
	f.BoolVar(&o.hide, "hide", false, "hide this native app pane while preserving its process")
	f.BoolVar(&o.status, "status", false, "inspect shared native pane state")
	f.BoolVar(&o.local, "local", false, "exerawr: bring retained local work into the center")
	f.BoolVar(&o.version, "version", false, "print build and tested Rex version")
	f.BoolVar(&o.help, "help", false, "show help")
	f.BoolVar(&o.help, "h", false, "show help")
	if name != "rawr" {
		f.BoolVar(&o.verbose, "verbose", false, "print successful pane action details as JSON")
		f.BoolVar(&o.verbose, "v", false, "print successful pane action details as JSON")
	}
	// Permit flags before or after subcommands while preserving literal argv after --.
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		key := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		definition := f.Lookup(key)
		if definition == nil {
			return o, nil, fmt.Errorf("unknown flag %s; use --help", arg)
		}
		flags = append(flags, arg)
		boolean, isBoolean := definition.Value.(interface{ IsBoolFlag() bool })
		if !strings.Contains(arg, "=") && !(isBoolean && boolean.IsBoolFlag()) {
			if i+1 == len(args) {
				return o, nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	if err := f.Parse(flags); err != nil {
		return o, nil, err
	}
	o.visited = map[string]bool{}
	f.Visit(func(value *flag.Flag) { o.visited[value.Name] = true })
	count := 0
	for _, value := range []bool{o.tui, o.toggle, o.hide, o.status, o.local} {
		if value {
			count++
		}
	}
	if count > 1 {
		return o, nil, errors.New("choose one of --tui, --toggle, --hide, --status or --local")
	}
	if o.contextFile != "" && !o.tui {
		return o, nil, errors.New("--context-file is only valid with --tui")
	}
	if o.expectedRevision < -1 {
		return o, nil, errors.New("--expected-revision must be nonnegative")
	}
	if o.viewFile != "" && (name != "filerawr" || count > 0 || o.contextFile != "") {
		return o, nil, errors.New("--view-file requires filerawr and cannot combine with pane/UI modes")
	}
	if (o.diff || o.staged) && o.viewFile == "" || o.staged && !o.diff {
		return o, nil, errors.New("--diff requires --view-file; --staged also requires --diff")
	}
	return o, positional, nil
}

func binaryDir() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Dir(executable)
}

func printResult(out io.Writer, value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usage(out io.Writer, name string) {
	if name == "rawr" {
		fmt.Fprintln(out, `Rawr: optional deliberately saved Rex projects

Usage:
  rawr create NAME [ROOT] [--apps filerawr,ghrawr,exerawr] [--repo OWNER/NAME]
  rawr list | show PROJECT
  rawr open PROJECT
  rawr save PROJECT [--expected-revision N]
  rawr plan PROJECT | restore PROJECT | status PROJECT
  rawr open-app PROJECT APP | toggle PROJECT APP | hide-app PROJECT APP
  rawr term PROJECT
  rawr link PROJECT EXISTING_VM
  rawr doctor

Global flags: --config-dir PATH --state-dir PATH --rex PATH --server ADDRESS
              --apps-state-dir PATH --client ID --root PATH --repo OWNER/NAME
              --vm NAME --version
Flags may appear before or after the command. APP is filerawr, ghrawr or exerawr.
Only save changes the deliberately saved arrangement. Restore preserves live work.
Go uses rawr-go directories and does not import Python state or edit Rex config.`)
		return
	}
	fmt.Fprintf(out, `%s: an independent Rex app

Usage: %s [--root PATH] [--repo OWNER/NAME] [--toggle | --hide | --status] [--verbose]
       %s --tui [--root PATH] [--repo OWNER/NAME]

Plain invocation opens or focuses a native pane and returns to the shell.
Successful pane actions are silent; --verbose (-v) prints their JSON details.
--toggle hides/shows; --hide retains its running program; --status reads state.
--status always prints JSON. Errors remain visible on stderr.
--tui runs in this terminal, including outside Rex. Rawr is optional.

Context: --scope NAME --session ID --anchor ID --state-dir PATH --vm NAME
Rex:     --rex PATH --server ADDRESS --client ID
Other:   --verbose (-v) --version --help
exerawr:  --local brings the retained local work back into the center.
filerawr: --view-file PATH [--diff [--staged]] opens a read-only viewer here.
REX_SESSION and REX_BLOCK supply the caller's session and working pane.
The project root stays anchored when focus moves into another shell.
`, name, name, name)
}
