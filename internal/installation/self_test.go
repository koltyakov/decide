package installation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

func selfFixture(t *testing.T) (source, dest, mainPackage string) {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "decide"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	t.Setenv("PATH", "")
	return exe, filepath.Join(t.TempDir(), "bin", name), info.Path
}

func TestInstallSelfCopiesAndReusesIdenticalBytes(t *testing.T) {
	source, dest, main := selfFixture(t)
	result, err := InstallSelf(context.Background(), source, dest, main)
	if err != nil || !result.Changed || result.Path != dest {
		t.Fatalf("%+v %v", result, err)
	}
	want, _ := os.ReadFile(source)
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, want) {
		t.Fatal("installed bytes differ")
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(dest); info.Mode().Perm()&0111 == 0 {
			t.Fatalf("not executable: %v", info.Mode())
		}
	}
	result, err = InstallSelf(context.Background(), source, dest, main)
	if err != nil || result.Changed {
		t.Fatalf("identical reinstall: %+v %v", result, err)
	}
	if runtime.GOOS != "windows" && result.OnPath {
		t.Fatal("empty PATH reported as containing the install directory")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".decide-*")); len(leftovers) != 0 {
		t.Fatalf("staging files left: %v", leftovers)
	}
}

func TestInstallSelfReplacesOlderBuildOfSameCommand(t *testing.T) {
	source, dest, main := selfFixture(t)
	old, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Trailing bytes keep the Go build metadata readable but change the checksum.
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dest, append(old, "older"...), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := InstallSelf(context.Background(), source, dest, main)
	if err != nil || !result.Changed {
		t.Fatalf("%+v %v", result, err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, old) {
		t.Fatal("older build not replaced")
	}
}

func TestInstallSelfRefusesUnrelatedDestination(t *testing.T) {
	source, dest, main := selfFixture(t)
	for name, prepare := range map[string]func() error{
		"foreign file": func() error { return os.WriteFile(dest, []byte("#!/bin/sh\necho other\n"), 0700) },
		"other command": func() error {
			b, err := os.ReadFile(source)
			if err != nil {
				return err
			}
			return os.WriteFile(dest, append(b, 'x'), 0700)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.RemoveAll(filepath.Dir(dest))
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				t.Fatal(err)
			}
			if err := prepare(); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(dest)
			want := main
			if name == "other command" {
				want = "example.com/other/cmd/decide"
			}
			if _, err := InstallSelf(context.Background(), source, dest, want); err == nil {
				t.Fatal("unrelated destination replaced")
			}
			if after, _ := os.ReadFile(dest); !bytes.Equal(before, after) {
				t.Fatal("unrelated destination modified")
			}
		})
	}
}

func TestInstallSelfRefusesSymlinkAndCancellation(t *testing.T) {
	source, dest, main := selfFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InstallSelf(ctx, source, dest, main); err == nil {
		t.Fatal("cancelled install ran")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("cancelled install wrote the destination")
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSelf(context.Background(), source, dest, main); err == nil {
		t.Fatal("symlink destination followed")
	}
}

func TestBinaryPathHonorsInstallDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DECIDE_INSTALL_DIR", dir)
	path, err := BinaryPath()
	if err != nil || filepath.Dir(path) != dir {
		t.Fatalf("%s %v", path, err)
	}
}
