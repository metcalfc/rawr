package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/metcalfc/rawr/rawrkit/rex"
)

// Exercise the actual CLI/manager path through a local Rex fixture. Quiet must
// suppress reporting without skipping the requested native operation.
func outputFixture(t *testing.T) ([]string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "rex.log")
	t.Setenv("RAWR_TEST_REX_LOG", log)
	cli := filepath.Join(dir, "rex")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$RAWR_TEST_REX_LOG"
if [ -n "$RAWR_TEST_REX_FAILURE" ]; then
  printf 'simulated Rex failure\n' >&2
  exit 1
fi
case "$*" in
  *whoami*) printf '%%s\n' '{"host_id":"host","instance_id":"generation","rex_version":"%s","endpoint":"unix:/tmp/fixture.sock"}' ;;
  *'api call session.view '*) printf '%%s\n' '{"session_id":"fixture","revision":1,"active_window_id":"work","windows":[{"window_id":"work","focused_block_id":"main","layers":[{"layer_id":"layer","kind":"tiled","layout":{"block_id":"main"},"blocks":[{"block_id":"main","host_id":"host","label":"Local work"}]}]}]}' ;;
  *'api call session.list_blocks '*) printf '%%s\n' '{"blocks":[{"block_id":"main","host_id":"host","label":"Local work","placement":"placed"}]}' ;;
  *'api call session.focus_block '*) printf '%%s\n' '{"revision":2}' ;;
  *) printf 'unexpected fixture request\n' >&2; exit 1 ;;
esac
`, rex.TestedVersion)
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return []string{"--root", dir, "--session", "fixture", "--anchor", "main", "--state-dir", filepath.Join(dir, "state"), "--rex", cli}, log
}

func TestAppLifecycleOutput(t *testing.T) {
	for _, name := range []string{"ghrawr", "filerawr", "exerawr", "ghrex", "filerex", "exerex"} {
		for _, mode := range []struct {
			name  string
			flags []string
			json  bool
		}{
			{"quiet", nil, false},
			{"verbose", []string{"--verbose"}, true},
			{"short", []string{"-v"}, true},
			{"disabled", []string{"--verbose=false"}, false},
		} {
			t.Run(name+"/"+mode.name, func(t *testing.T) {
				args, log := outputFixture(t)
				args = append(args, "--hide")
				args = append(args, mode.flags...)
				var out, stderr bytes.Buffer
				if code := Run(context.Background(), name, args, &out, &stderr); code != 0 || stderr.Len() != 0 {
					t.Fatalf("code=%d stderr=%s", code, &stderr)
				}
				if mode.json {
					var got struct {
						Result string `json:"result"`
					}
					if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Result != "complete" {
						t.Fatalf("missing action details: %s (%v)", &out, err)
					}
				} else if out.Len() != 0 {
					t.Fatalf("quiet command printed %q", out.String())
				}
				calls, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(calls), "api call session.list_blocks ") {
					t.Fatal("quiet mode skipped the pane operation")
				}
			})
		}
	}
}

func TestExplicitAppStatusStillPrintsJSON(t *testing.T) {
	for _, flags := range [][]string{{"--status"}, {"--status", "--verbose"}} {
		args, _ := outputFixture(t)
		args = append(args, flags...)
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), "ghrawr", args, &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stderr=%s", code, &stderr)
		}
		var status rexStatus
		if err := json.Unmarshal(out.Bytes(), &status); err != nil || status.Main != "main" {
			t.Fatalf("status output: %s (%v)", &out, err)
		}
	}
}

type rexStatus struct {
	Main string `json:"main_block"`
}

func TestLocalReturnOutputAndErrors(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		args, log := outputFixture(t)
		args = append(args, "--local")
		if verbose {
			args = append(args, "--verbose")
		}
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), "exerawr", args, &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stderr=%s", code, &stderr)
		}
		if (out.Len() > 0) != verbose {
			t.Fatalf("verbose=%v stdout=%q", verbose, &out)
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(calls), "api call session.focus_block ") {
			t.Fatal("silent return did not focus local work")
		}
		t.Setenv("RAWR_TEST_REX_FAILURE", "1")
		out.Reset()
		stderr.Reset()
		if code := Run(context.Background(), "exerawr", args, &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "simulated Rex failure") {
			t.Fatalf("hidden failure: code=%d stdout=%q stderr=%q", code, &out, &stderr)
		}
		t.Setenv("RAWR_TEST_REX_FAILURE", "")
	}
}
