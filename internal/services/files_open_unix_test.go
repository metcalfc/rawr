//go:build unix

package services

import (
	"context"
	"golang.org/x/sys/unix"
	"path/filepath"
	"testing"
	"time"
)

func TestFilePreviewRefusesFifoWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pipe")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadFileDocument(context.Background(), root, path, false, false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("file preview blocked on FIFO")
	}
}
