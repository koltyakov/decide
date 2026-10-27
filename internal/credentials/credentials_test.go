package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreAndEnvironment(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "private", "credentials.json")}
	t.Setenv("TYPESAFE_API_KEY", "")
	if err := s.Set("official", "secret-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("openrouter", "secret-two"); err != nil {
		t.Fatal(err)
	}
	key, source, err := s.Get("official", "TYPESAFE_API_KEY")
	if err != nil || key != "secret-one" || source != "store" {
		t.Fatalf("%q %q %v", key, source, err)
	}
	t.Setenv("TYPESAFE_API_KEY", "environment-key")
	key, source, err = s.Get("official", "TYPESAFE_API_KEY")
	if err != nil || key != "environment-key" || source != "environment" {
		t.Fatalf("%q %q %v", key, source, err)
	}
	if runtime.GOOS != "windows" {
		for _, tt := range []struct {
			path string
			mode os.FileMode
		}{{s.Path, 0600}, {filepath.Dir(s.Path), 0700}} {
			info, err := os.Stat(tt.path)
			if err != nil || info.Mode().Perm() != tt.mode {
				t.Fatalf("unsafe permissions on %s", tt.path)
			}
		}
	}
	if err := s.Remove("official"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, _, err := s.Get("official", "TYPESAFE_API_KEY"); err == nil {
		t.Error("removed credential still present")
	}
	if key, _, err := s.Get("openrouter", "TYPESAFE_API_KEY"); err != nil || key != "secret-two" {
		t.Error("removal damaged another provider")
	}
}

func TestMetadataAndCorruption(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := os.WriteFile(s.Path, []byte(`{"providers":{"official":"saved-key"},"metadata":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_KEY", "")
	if key, _, err := s.Get("official", "TEST_KEY"); err != nil || key != "saved-key" {
		t.Fatal("saved key not read", err)
	}
	if err := s.Set("custom", "new-key"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(b), "metadata") {
		t.Error("metadata lost")
	}
	for _, broken := range []string{`null`, `[]`, `{"providers":null}`, `{"providers":[]}`, `not JSON`} {
		if err := os.WriteFile(s.Path, []byte(broken), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("official", "new-key"); err == nil {
			t.Errorf("overwrote corrupted store %s", broken)
		}
		b, _ := os.ReadFile(s.Path)
		if string(b) != broken {
			t.Error("corrupted store changed")
		}
	}
}

func TestDefaultPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path, err := DefaultPath()
	if err != nil || path != filepath.Join(base, "decide", "credentials.json") {
		t.Fatalf("%s %v", path, err)
	}
}
