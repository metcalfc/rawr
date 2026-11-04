//go:build unix

package services

import (
	"golang.org/x/sys/unix"
	"os"
)

// Nonblocking open prevents a raced-in FIFO/device from hanging before f.Stat.
func openRootFile(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
