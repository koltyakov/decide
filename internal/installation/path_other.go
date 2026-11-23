//go:build !windows

package installation

// Unix shells own PATH configuration; install-self only reports whether the
// directory is already present.
func addUserPath(string) (bool, error) { return false, nil }
