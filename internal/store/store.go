// Package store provides atomic private file writes and advisory edit locks.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const lockWait = 5 * time.Second

func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".decide.lock")
	// Wait briefly, so concurrent edits serialize instead of failing at once.
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if errors.Is(err, context.DeadlineExceeded) {
		ok, err = false, nil
	}
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
