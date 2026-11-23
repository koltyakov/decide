//go:build !windows

package store

import "os"

// syncDirectory makes a completed rename durable across power loss.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
