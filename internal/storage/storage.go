package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ReadJSON(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return fmt.Errorf("state file exceeds 8 MiB: %s", path)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing data in %s", path)
	}
	return nil
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 8<<20 {
		return fmt.Errorf("state document exceeds 8 MiB; previous file retained")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func WithLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = lockFile(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlockFile(f)
	return fn()
}
