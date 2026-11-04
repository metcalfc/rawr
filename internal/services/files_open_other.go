//go:build !unix

package services

import "os"

func openRootFile(root *os.Root, path string) (*os.File, error) { return root.Open(path) }
