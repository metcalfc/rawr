//go:build windows

package storage

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"time"
)

func lockFile(f *os.File) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("another command is still operating on this workspace")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func syncDir(string) error { return nil }
