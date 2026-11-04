package rex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func identityJSON(instance string) []byte {
	b, _ := json.Marshal(Identity{HostID: "host", InstanceID: instance, Version: TestedVersion, Endpoint: "unix:/tmp/rex socket"})
	return b
}
func testRunnerResponse(c *Client, raw string) {
	c.Runner = func(context.Context, string, []string) ([]byte, []byte, error) { return []byte(raw), nil, nil }
}

const validView = `{"session_id":"s","revision":4,"active_window_id":"w","windows":[{"window_id":"w","focused_block_id":"b","layers":[{"layer_id":"l","kind":"tiled","layout":{"block_id":"b"},"blocks":[{"block_id":"b","host_id":"host","creator_name":"com.superlogical.terminal","flavor":"com.superlogical.terminal.shell"}]}]}]}`

func TestIdentityRequiresTestedVersionAndLocalGeneration(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity Identity
		want     string
	}{
		{"version", Identity{Version: "future", HostID: "host", InstanceID: "instance", Endpoint: "unix:/tmp/a"}, "unsupported Rex version"},
		{"remote", Identity{Version: TestedVersion, HostID: "host", InstanceID: "instance", Endpoint: "tcp:remote:123"}, "verified local Unix"},
		{"missing host", Identity{Version: TestedVersion, InstanceID: "instance", Endpoint: "unix:/tmp/a"}, "host/generation"},
		{"missing generation", Identity{Version: TestedVersion, HostID: "host", Endpoint: "unix:/tmp/a"}, "host/generation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := New("rex", "", "")
			b, _ := json.Marshal(test.identity)
			testRunnerResponse(c, string(b))
			if _, err := c.Identity(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("identity accepted: %v", err)
			}
		})
	}
}

func TestViewRejectsMalformedMissingAndStaleResults(t *testing.T) {
	for _, raw := range []string{
		`not-json`, `null`, `{"session_id":"other","revision":0,"windows":[]}`,
		`{"session_id":"s","windows":[]}`, `{"session_id":"s","revision":0}`,
		`{"session_id":"s","revision":-1,"windows":[]}`,
		`{"session_id":"s","revision":0,"windows":[{}]}`,
		`{"session_id":"s","revision":0,"windows":[{"window_id":"w","layers":[{"layout":{},"blocks":[]}]}]}`,
		`{"session_id":"s","revision":0,"windows":[{"window_id":"w","layers":[{"layout":{"block_id":"b"},"blocks":[{"block_id":"b"},{"block_id":"b"}]}]}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			c := New("rex", "", "")
			testRunnerResponse(c, raw)
			if _, err := c.View(context.Background(), "s"); err == nil {
				t.Fatalf("accepted malformed view: %s", raw)
			}
		})
	}
	c := New("rex", "", "")
	testRunnerResponse(c, validView)
	if v, err := c.View(context.Background(), "s"); err != nil || v.Focused() != "b" || v.Blocks()["b"].HostID != "host" {
		t.Fatalf("valid view decode: %+v %v", v, err)
	}
	testRunnerResponse(c, strings.Replace(validView, `"revision":4`, `"revision":3`, 1))
	if _, err := c.View(context.Background(), "s"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("accepted old view: %v", err)
	}
	c.Runner = func(context.Context, string, []string) ([]byte, []byte, error) {
		return identityJSON("new-generation"), nil, nil
	}
	if _, err := c.Identity(context.Background()); err != nil {
		t.Fatal(err)
	}
	testRunnerResponse(c, strings.Replace(validView, `"revision":4`, `"revision":0`, 1))
	if _, err := c.View(context.Background(), "s"); err != nil {
		t.Fatalf("generation reset retained old revision: %v", err)
	}
}

func TestSanitizedNativeViewSchemaDecodesExactly(t *testing.T) {
	// API field names and native split shape are preserved; every identifier is synthetic.
	data, err := os.ReadFile("testdata/session-view.json")
	if err != nil {
		t.Fatal(err)
	}
	var original View
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	c := New("rex", "", "")
	testRunnerResponse(c, string(data))
	view, err := c.View(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != 8 || len(view.Windows) != 1 || view.Focused() == "" || len(view.Blocks()) < 3 {
		t.Fatalf("native schema lost fields: %+v", view)
	}
	for _, block := range view.Blocks() {
		if block.HostID != "host:fixture" || block.Creator != "com.superlogical.terminal" || block.Flavor != "com.superlogical.terminal.shell" {
			t.Fatalf("native block fields lost: %+v", block)
		}
	}
	layers := view.Windows[0].Layers
	if len(layers) != 2 || layers[0].Kind != "tiled" || layers[1].Kind != "floating" || layers[0].Layout.Split.Direction != "vertical" || layers[0].Layout.Split.Ratio != .6 {
		t.Fatal("native layer/split geometry lost")
	}
	for _, id := range Leaves(layers[0].Layout) {
		if _, exists := view.Blocks()[id]; !exists {
			t.Fatalf("layout leaf has no matching block: %s", id)
		}
	}
}

func TestRunPassesLiteralExecutableEndpointAndJSON(t *testing.T) {
	executable := "/tmp/Rex 雪 ; $(literal)/rex"
	server := "unix:/tmp/rex socket ; $(literal)"
	c := New(executable, server, "")
	var gotExe string
	var gotArgs []string
	c.Runner = func(_ context.Context, exe string, args []string) ([]byte, []byte, error) {
		gotExe = exe
		gotArgs = append([]string(nil), args...)
		return []byte(`{"ok":true}`), nil, nil
	}
	args := map[string]any{"cwd": "/tmp/project ; $(literal)", "command": []string{"/tmp/exe with spaces", "literal;$(x)"}}
	var result map[string]bool
	if err := c.Call(context.Background(), "session.new", args, &result); err != nil {
		t.Fatal(err)
	}
	if gotExe != executable || !reflect.DeepEqual(gotArgs[:7], []string{"--autostart=false", "--timeout", "8s", "-S", server, "api", "call"}) || gotArgs[7] != "session.new" || len(gotArgs) != 9 {
		t.Fatalf("argv changed: %q %q", gotExe, gotArgs)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(gotArgs[8]), &decoded); err != nil || decoded["cwd"] != args["cwd"] {
		t.Fatalf("JSON literal changed: %s", gotArgs[8])
	}
}

func TestTimeoutNeverRetriesPotentialCreation(t *testing.T) {
	c := New("rex", "", "")
	calls := 0
	c.Runner = func(ctx context.Context, _ string, _ []string) ([]byte, []byte, error) {
		calls++
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "session.new", map[string]any{}, nil)
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "creation may have succeeded") {
		t.Fatalf("unsafe retry/timeout: calls=%d err=%v", calls, err)
	}
}

func TestMalformedResultNeverRetriesAndAmbiguousAppDoesNotSelect(t *testing.T) {
	c := New("rex", "", "")
	calls := 0
	c.Runner = func(context.Context, string, []string) ([]byte, []byte, error) {
		calls++
		return []byte("bad-json"), nil, nil
	}
	if _, err := c.Sessions(context.Background()); err == nil || calls != 1 {
		t.Fatalf("malformed result was accepted/retried: %d %v", calls, err)
	}
	calls = 0
	c.Runner = func(context.Context, string, []string) ([]byte, []byte, error) {
		calls++
		return []byte(`{"clients":[{"client_id":"one","info":{"kind":"app","label":"one"}},{"client_id":"two","info":{"kind":"app","label":"two"}}]}`), nil, nil
	}
	if err := c.Select(context.Background(), "s", "b"); err == nil || !strings.Contains(err.Error(), "multiple Rex apps") || calls != 1 {
		t.Fatalf("ambiguous app selection mutated focus: %d %v", calls, err)
	}
}

func TestLayoutRejectsAmbiguousOrInvalidGeometry(t *testing.T) {
	leaf := func() *LayoutNode { return &LayoutNode{BlockID: "b"} }
	for _, node := range []*LayoutNode{
		{}, {BlockID: "b", Split: &Split{}}, {Split: &Split{Direction: "diagonal", Ratio: .5, Before: leaf(), After: leaf()}},
		{Split: &Split{Direction: "horizontal", Ratio: 0, Before: leaf(), After: leaf()}},
		{Split: &Split{Direction: "vertical", Ratio: 1, Before: leaf(), After: leaf()}},
		{Split: &Split{Direction: "horizontal", Ratio: .5, Before: leaf()}},
	} {
		if err := ValidateLayout(node, 0); err == nil {
			t.Fatalf("accepted invalid split: %+v", node)
		}
	}
	if err := ValidateLayout(leaf(), 41); err == nil {
		t.Fatal("accepted excessive depth")
	}
}

func TestRexErrorsReturnNoMutationRetry(t *testing.T) {
	c := New("rex", "", "")
	calls := 0
	c.Runner = func(context.Context, string, []string) ([]byte, []byte, error) {
		calls++
		return nil, []byte("failed request"), errors.New("exit 1")
	}
	if err := c.Call(context.Background(), "session.new", nil, nil); err == nil || calls != 1 {
		t.Fatalf("unsafe failure handling: %d %v", calls, err)
	}
}
