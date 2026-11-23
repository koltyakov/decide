//go:build windows

package installation

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// addUserPath appends dir to the user PATH and keeps the value's registry type,
// so %VARIABLE% entries stay unexpanded.
func addUserPath(dir string) (bool, error) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer func() { _ = key.Close() }()
	value, kind, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		value, kind, err = "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return false, err
	}
	var entries []string
	for entry := range strings.SplitSeq(value, ";") {
		if entry == "" {
			continue
		}
		expanded, err := registry.ExpandString(entry)
		if err != nil {
			expanded = entry
		}
		if strings.EqualFold(strings.TrimRight(expanded, `\`), strings.TrimRight(dir, `\`)) {
			return false, nil
		}
		entries = append(entries, entry)
	}
	value = strings.Join(append(entries, dir), ";")
	if kind == registry.SZ {
		return true, key.SetStringValue("Path", value)
	}
	return true, key.SetExpandStringValue("Path", value)
}
