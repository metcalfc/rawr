//go:build !windows

package storage

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"time"
)

func lockFile(f *os.File) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("another command is still operating on this workspace")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
func unlockFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
