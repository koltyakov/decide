//go:build windows

package store

// Windows commits the rename's directory entry with the file; directories
// cannot be opened for Sync.
func syncDirectory(string) error { return nil }

// SyncDirectory makes a completed rename in path durable where supported.
func SyncDirectory(path string) error { return syncDirectory(path) }
