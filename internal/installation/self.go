package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/koltyakov/decide/internal/store"
)

// MainPackage identifies decide executables by their Go build metadata.
const MainPackage = "github.com/koltyakov/decide/cmd/decide"

// SelfResult describes an install-self run.
type SelfResult struct {
	Path      string `json:"path"`
	Changed   bool   `json:"changed"`
	PathAdded bool   `json:"path_added"`
	OnPath    bool   `json:"on_path"`
}

// BinaryPath is the per-user destination shared with the release installers:
// DECIDE_INSTALL_DIR, else ~/.local/bin or %LOCALAPPDATA%\Programs\decide.
func BinaryPath() (string, error) {
	dir := os.Getenv("DECIDE_INSTALL_DIR")
	if dir == "" {
		if runtime.GOOS == "windows" {
			local := os.Getenv("LOCALAPPDATA")
			if local == "" {
				return "", errors.New("LOCALAPPDATA is not set; set DECIDE_INSTALL_DIR")
			}
			dir = filepath.Join(local, "Programs", "decide")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".local", "bin")
		}
	}
	name := "decide"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Abs(filepath.Join(dir, name))
}

// InstallSelf copies source to dest and, on Windows, adds dest's directory to
// the user PATH. An existing destination is never executed: identical bytes are
// reused, and other files are replaced only when their Go build metadata names
// mainPackage.
func InstallSelf(ctx context.Context, source, dest, mainPackage string) (SelfResult, error) {
	result := SelfResult{Path: dest}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return result, err
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return result, err
	}
	if !sourceInfo.Mode().IsRegular() {
		return result, errors.New("source executable is not a regular file")
	}
	existing, err := os.Lstat(dest)
	switch {
	case errors.Is(err, os.ErrNotExist):
		existing = nil
	case err != nil:
		return result, err
	case !existing.Mode().IsRegular():
		return result, errors.New("installation destination is not a regular file: " + dest)
	}
	if existing == nil || !os.SameFile(sourceInfo, existing) {
		same := false
		if existing != nil {
			if same, err = sameContent(source, dest); err != nil {
				return result, err
			}
			if !same {
				if info, err := buildinfo.ReadFile(dest); err != nil || info.Path != mainPackage {
					return result, errors.New("existing executable is not a decide installation: " + dest)
				}
			}
		}
		if !same {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := replaceBinary(source, dest, existing != nil); err != nil {
				return result, err
			}
			result.Changed = true
		}
	}
	dir := filepath.Dir(dest)
	if result.PathAdded, err = addUserPath(dir); err != nil {
		return result, fmt.Errorf("installed %s but could not update the user PATH: %w", dest, err)
	}
	result.OnPath = result.PathAdded || onPath(dir)
	return result, nil
}

func sameContent(a, b string) (bool, error) {
	ha, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ha, hb), nil
}

func fileSHA256(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// replaceBinary stages a complete, synced copy beside dest and then renames it
// into place, so an interrupted install never leaves a partial executable.
func replaceBinary(source, dest string, exists bool) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	staged, err := os.CreateTemp(dir, ".decide-install-*"+filepath.Ext(dest))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged.Name()) }()
	defer func() { _ = staged.Close() }()
	if _, err = io.Copy(staged, in); err != nil {
		return err
	}
	if err = staged.Chmod(0755); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if err = staged.Sync(); err != nil {
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	if same, err := sameContent(source, staged.Name()); err != nil || !same {
		return errors.Join(errors.New("staged executable does not match the source"), err)
	}
	if runtime.GOOS != "windows" || !exists {
		if err = os.Rename(staged.Name(), dest); err != nil {
			return err
		}
		_ = store.SyncDirectory(dir)
		return nil
	}
	return replaceRunningWindows(staged.Name(), dest)
}

// Windows can rename a running executable, such as one serving MCP, but not
// overwrite it. Move it aside, restore it if the swap fails, and remove
// leftovers from earlier upgrades once they are unlocked.
func replaceRunningWindows(staged, dest string) error {
	dir := filepath.Dir(dest)
	aside, err := os.CreateTemp(dir, ".decide-old-*.exe")
	if err != nil {
		return err
	}
	previous := aside.Name()
	_ = aside.Close()
	_ = os.Remove(previous)
	if err = os.Rename(dest, previous); err != nil {
		return err
	}
	if err = os.Rename(staged, dest); err != nil {
		return errors.Join(err, os.Rename(previous, dest))
	}
	if old, _ := filepath.Glob(filepath.Join(dir, ".decide-old-*.exe")); len(old) > 0 {
		for _, path := range old {
			_ = os.Remove(path)
		}
	}
	return nil
}

func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if abs, err := filepath.Abs(entry); err == nil && samePath(abs, dir) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
