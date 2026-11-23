//go:build !windows

package store

import "os"

// SyncDirectory makes a completed rename in path durable where supported.
func SyncDirectory(path string) error { return syncDirectory(path) }

// syncDirectory makes a completed rename durable across power loss.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
