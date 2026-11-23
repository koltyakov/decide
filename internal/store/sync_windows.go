//go:build windows

package store

// Windows commits the rename's directory entry with the file; directories
// cannot be opened for Sync.
func syncDirectory(string) error { return nil }
