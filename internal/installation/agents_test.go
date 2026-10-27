package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

func TestInstallAllClientsAndScopes(t *testing.T) {
	for _, client := range []string{"opencode", "claude", "cursor", "codex", "copilot", "devin", "windsurf", "antigravity", "agents"} {
		for _, global := range []bool{true, false} {
			t.Run(client+map[bool]string{true: "/global", false: "/project"}[global], func(t *testing.T) {
				home, project := t.TempDir(), ""
				if !global {
					project = t.TempDir()
				}
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
				t.Setenv("APPDATA", filepath.Join(home, "appdata"))
				o := Options{Client: client, Home: home, Project: project, Binary: "/path with spaces/decide", Args: []string{"--credentials", "/private/credentials.json", "mcp"}, MCP: true, Skill: true, Direct: true}
				paths, err := Install(context.Background(), o)
				if client == "windsurf" && !global {
					if err == nil || !strings.Contains(err.Error(), "devin") {
						t.Fatal("undocumented project MCP installation succeeded", err)
					}
					if _, statErr := os.Stat(paths.Skill); !os.IsNotExist(statErr) {
						t.Fatal("rejected installation wrote a skill", statErr)
					}
					o.MCP = false
					paths, err = Install(context.Background(), o)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(paths.Skill); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(paths.MCP)
				if err != nil {
					t.Fatal(err)
				}
				root := map[string]any{}
				if paths.TOML {
					err = toml.Unmarshal(b, &root)
					if root["mcp_servers"].(map[string]any)["decide"] == nil {
						t.Error("missing Codex entry")
					}
				} else {
					err = json.Unmarshal(b, &root)
					var current any = root
					for _, part := range append(paths.ServerPath, "decide") {
						current = current.(map[string]any)[part]
					}
					if current == nil {
						t.Error("missing server entry")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(paths.Skill)
				if _, err = Install(context.Background(), o); err != nil {
					t.Fatal("reinstall", err)
				}
				after, _ := os.ReadFile(paths.Skill)
				if !bytes.Equal(before, after) || !bytes.Contains(before, []byte("Managed by decide")) {
					t.Error("skill is not idempotent or managed")
				}
			})
		}
	}
}

func TestCurrentDiscoveryLocations(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	devinHome := filepath.Join(home, "xdg")
	if runtime.GOOS == "windows" {
		devinHome = filepath.Join(home, "appdata")
	}
	for _, tt := range []struct{ client, project, mcp, skill string }{
		{"codex", "", filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".agents", "skills", "decide", "SKILL.md")},
		{"codex", project, filepath.Join(project, ".codex", "config.toml"), filepath.Join(project, ".agents", "skills", "decide", "SKILL.md")},
		{"devin", "", filepath.Join(devinHome, "devin", "mcp_config.json"), filepath.Join(devinHome, "devin", "skills", "decide", "SKILL.md")},
		{"devin", project, filepath.Join(project, ".devin", "mcp_config.json"), filepath.Join(project, ".devin", "skills", "decide", "SKILL.md")},
		{"antigravity", "", filepath.Join(home, ".gemini", "config", "mcp_config.json"), filepath.Join(home, ".gemini", "config", "skills", "decide", "SKILL.md")},
		{"antigravity", project, filepath.Join(project, ".agents", "mcp_config.json"), filepath.Join(project, ".agents", "skills", "decide", "SKILL.md")},
	} {
		paths, err := Locations(tt.client, home, tt.project)
		if err != nil || paths.MCP != tt.mcp || paths.Skill != tt.skill {
			t.Fatalf("%s: %+v %v", tt.client, paths, err)
		}
	}
}

func TestSkillPathUpdateDoesNotDeleteLegacyCopy(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".codex", "skills", "decide", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("user-maintained legacy skill")
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := Install(context.Background(), Options{Client: "codex", Home: home, Skill: true})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(before, original) {
		t.Fatal("legacy skill modified", err)
	}
	if paths.Skill == legacy {
		t.Fatal("legacy discovery path reused")
	}
}

func TestJSONCPreservesCommentsAndUnrelatedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	paths, _ := Locations("opencode", home, "")
	paths.MCP += "c"
	original := []byte("{\n // keep comment\n \"model\":\"test/model\",\n \"mcp\":{\"servers\":{\"other\":{\"command\":[\"other\"]},\"jev\":{\"command\":[\"jev\",\"mcp\"]}}},\n}\n")
	if err := os.MkdirAll(filepath.Dir(paths.MCP), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MCP, original, 0600); err != nil {
		t.Fatal(err)
	}
	o := Options{Client: "opencode", Home: home, Binary: "/bin/decide", Args: []string{"mcp"}, MCP: true, Direct: true}
	got, err := Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got.MCP != paths.MCP {
		t.Fatal("did not prefer JSONC")
	}
	b, _ := os.ReadFile(paths.MCP)
	if !bytes.Contains(b, []byte("keep comment")) || !bytes.Contains(b, []byte("other")) || !bytes.Contains(b, []byte("test/model")) {
		t.Error("lost unrelated configuration")
	}
	backup, err := os.ReadFile(paths.MCP + ".decide-backup")
	if err != nil || !bytes.Equal(backup, original) {
		t.Error("backup does not match original")
	}
	value, err := hujson.Parse(b)
	if err != nil || value.Find("/mcp/servers/decide") == nil {
		t.Fatal("invalid V2 configuration", err)
	}
	legacy := value.Find("/mcp/servers/jev/command/0")
	if legacy == nil || string(legacy.Pack()) != `"jev"` {
		t.Fatal("existing legacy MCP registration changed")
	}
}

func TestRefusesUnmanagedSkillsAndInvalidParents(t *testing.T) {
	home := t.TempDir()
	paths, _ := Locations("agents", home, "")
	if err := os.MkdirAll(filepath.Dir(paths.Skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Skill, []byte("user's skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), Options{Client: "agents", Home: home, Skill: true}); err == nil {
		t.Error("overwrote an unmanaged skill")
	}
	for _, invalid := range []string{`[]`, `{"mcpServers":null}`, `{"mcpServers":[]}`, `broken`} {
		if err := os.WriteFile(paths.MCP, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Install(context.Background(), Options{Client: "agents", Home: home, MCP: true, Direct: true}); err == nil {
			t.Error("overwrote malformed config", invalid)
		}
		b, _ := os.ReadFile(paths.MCP)
		if strings.TrimSpace(string(b)) != invalid {
			t.Error("invalid file changed")
		}
	}
}
