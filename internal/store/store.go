// Package store provides atomic private file writes and advisory edit locks.
package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".decide.lock")
	ok, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !ok {
		_ = lock.Close()
		return nil, fmt.Errorf("file is being edited: %s", path)
	}
	return func() { _ = lock.Close() }, nil
}

func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".decide-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
