package installation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/koltyakov/decide/skills"
)

func TestRefreshSkillsUpdatesOnlyExistingManagedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, client := range []string{"claude", "codex"} {
		if _, err := Install(context.Background(), Options{Client: client, Home: home, Skill: true}); err != nil {
			t.Fatal(err)
		}
	}
	claude, _ := Locations("claude", home, "")
	codex, _ := Locations("codex", home, "")
	cursor, _ := Locations("cursor", home, "")
	stale := []byte("---\nname: decide\n---\n" + ManagedMarker + "\nold guidance\n")
	if err := os.WriteFile(claude.Skill, stale, 0600); err != nil {
		t.Fatal(err)
	}
	unmanaged := []byte("my own decide notes\n")
	if err := os.WriteFile(codex.Skill, unmanaged, 0600); err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshSkills(home, "")
	if err != nil || len(refreshed) != 1 || refreshed[0] != claude.Skill {
		t.Fatalf("%v %v", refreshed, err)
	}
	if b, _ := os.ReadFile(claude.Skill); !bytes.Equal(b, skills.Decide) {
		t.Fatal("managed skill not refreshed")
	}
	if b, _ := os.ReadFile(codex.Skill); !bytes.Equal(b, unmanaged) {
		t.Fatal("unmanaged skill overwritten")
	}
	if _, err = os.Stat(cursor.Skill); !os.IsNotExist(err) {
		t.Fatal("refresh created a new integration")
	}
	if refreshed, err = RefreshSkills(home, ""); err != nil || len(refreshed) != 0 {
		t.Fatal("refresh is not idempotent", refreshed, err)
	}
}

func TestRefreshSkillsIgnoresSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	paths, _ := Locations("cursor", home, "")
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	stale := []byte(ManagedMarker + "\nold\n")
	if err := os.WriteFile(target, stale, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.Skill); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := RefreshSkills(home, ""); err != nil || len(refreshed) != 0 {
		t.Fatal("refresh followed a symlink", refreshed, err)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, stale) {
		t.Fatal("symlink target changed")
	}
}
