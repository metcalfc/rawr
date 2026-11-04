package storage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type fixtureState struct {
	Sequence int    `json:"sequence"`
	Payload  string `json:"payload"`
}

func TestReadJSONRejectsPartialTrailingUnknownAndOversizeData(t *testing.T) {
	for _, test := range []struct{ name, data string }{
		{"partial", `{"sequence":1`},
		{"trailing object", `{"sequence":1} {"sequence":2}`},
		{"trailing junk", `{"sequence":1} junk`},
		{"unknown schema field", `{"sequence":1,"future_mutation_recipe":true}`},
		{"oversize after valid document", `{"sequence":1}` + strings.Repeat(" ", 8<<20) + "JUNK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			var state fixtureState
			if err := ReadJSON(path, &state); err == nil {
				t.Fatalf("accepted invalid state: %s", test.name)
			}
		})
	}
}

func TestAtomicWritesNeverExposePartialStateToReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteJSON(path, fixtureState{Payload: strings.Repeat("0", 4096)}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	errors := make(chan error, 1)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				var state fixtureState
				if err := ReadJSON(path, &state); err != nil {
					select {
					case errors <- err:
					default:
					}
					return
				}
				if state.Payload != strings.Repeat(fmt.Sprint(state.Sequence), 4096) {
					select {
					case errors <- fmt.Errorf("torn state sequence=%d", state.Sequence):
					default:
					}
					return
				}
			}
		}()
	}
	for sequence := 1; sequence <= 30; sequence++ {
		if err := WriteJSON(path, fixtureState{Sequence: sequence, Payload: strings.Repeat(fmt.Sprint(sequence), 4096)}); err != nil {
			// Windows may refuse replacing a file held open by another process;
			// this remains an error, and must never expose a partial replacement.
			if runtime.GOOS != "windows" {
				close(done)
				readers.Wait()
				t.Fatal(err)
			}
		}
	}
	close(done)
	readers.Wait()
	select {
	case err := <-errors:
		t.Fatal(err)
	default:
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("state permissions: %v %v", info, err)
		}
	}
}

func TestFailedSerializationKeepsPreviousStateAndCleansTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := WriteJSON(path, fixtureState{Sequence: 7, Payload: "previous"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, map[string]any{"unserializable": make(chan int)}); err == nil {
		t.Fatal("expected marshal failure")
	}
	var state fixtureState
	if err := ReadJSON(path, &state); err != nil || state.Sequence != 7 || state.Payload != "previous" {
		t.Fatalf("failed write damaged prior state: %+v %v", state, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("temporary files leaked: %+v", entries)
	}
	destination := filepath.Join(dir, "directory.json")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(destination, state); err == nil {
		t.Fatal("expected rename failure onto directory")
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".write-") {
			t.Fatal("failed rename left a temporary state file")
		}
	}
}

func TestOversizedWritePreservesReadablePreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	previous := fixtureState{Sequence: 7, Payload: "previous"}
	if err := WriteJSON(path, previous); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, fixtureState{Payload: strings.Repeat("x", 8<<20)}); err == nil {
		t.Fatal("write accepted a state document larger than its reader permits")
	}
	var state fixtureState
	if err := ReadJSON(path, &state); err != nil || state != previous {
		t.Fatalf("oversized write replaced usable state: %+v %v", state, err)
	}
}

func TestLockIsReleasedAfterCallbackError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	sentinel := errors.New("callback failed")
	if err := WithLock(path, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error changed: %v", err)
	}
	called := false
	if err := WithLock(path, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("error path retained lock: %v", err)
	}
}

func TestStorageLockProcessHelper(t *testing.T) {
	path := os.Getenv("RAWR_STORAGE_LOCK_TEST_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	for i := 0; i < 3; i++ {
		if err := WithLock(path+".lock", func() error {
			var state fixtureState
			if err := ReadJSON(path, &state); err != nil {
				return err
			}
			state.Sequence++
			return WriteJSON(path, state)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentCommandLocksPreventLostUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-state.json")
	if err := WriteJSON(path, fixtureState{}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 6)
	for i := range commands {
		commands[i] = exec.Command(executable, "-test.run=^TestStorageLockProcessHelper$", "-test.count=1")
		commands[i].Env = append(os.Environ(), "RAWR_STORAGE_LOCK_TEST_PATH="+path)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("concurrent command: %v", err)
		}
	}
	var state fixtureState
	if err := ReadJSON(path, &state); err != nil {
		t.Fatal(err)
	}
	if state.Sequence != 18 {
		t.Fatalf("lost concurrent state updates: sequence=%d want 18", state.Sequence)
	}
}
