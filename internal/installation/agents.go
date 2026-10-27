// Package installation installs MCP configuration and managed skills for AI clients.
package installation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/koltyakov/decide/internal/store"
	"github.com/koltyakov/decide/skills"
	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

type Paths struct {
	MCP, Skill string
	ServerPath []string
	TOML       bool
}

// Locations follows Control's global and project-local client integration layout.
func Locations(client, home, project string) (Paths, error) {
	global := project == ""
	base := home
	if !global {
		base = project
	}
	var config, skill string
	server := []string{"mcpServers"}
	switch client {
	case "opencode":
		if global {
			base = filepath.Join(home, ".config")
			if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
				base = value
			}
			config, skill = "opencode/opencode.json", "opencode/skills/decide"
		} else {
			config, skill = "opencode.json", ".opencode/skills/decide"
		}
		server = []string{"mcp", "servers"}
	case "claude":
		config, skill = ".mcp.json", ".claude/skills/decide"
		if global {
			config = ".claude.json"
		}
	case "cursor":
		config, skill = ".cursor/mcp.json", ".cursor/skills/decide"
	case "copilot":
		config, skill = ".copilot/mcp-config.json", ".copilot/skills/decide"
		if !global {
			config, skill, server = ".vscode/mcp.json", ".github/skills/decide", []string{"servers"}
		}
	case "codex":
		config, skill = ".codex/config.toml", ".agents/skills/decide"
	case "devin":
		config, skill = ".devin/mcp_config.json", ".devin/skills/decide"
		if global {
			base = filepath.Join(home, ".config")
			if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
				base = value
			}
			if runtime.GOOS == "windows" {
				base = os.Getenv("APPDATA")
				if base == "" {
					base = filepath.Join(home, "AppData", "Roaming")
				}
			}
			config, skill = "devin/mcp_config.json", "devin/skills/decide"
		}
	case "windsurf":
		config, skill = ".codeium/windsurf/mcp_config.json", ".codeium/windsurf/skills/decide"
		if !global {
			config, skill = "", ".windsurf/skills/decide"
		}
	case "antigravity":
		config, skill = ".gemini/config/mcp_config.json", ".gemini/config/skills/decide"
		if !global {
			config, skill = ".agents/mcp_config.json", ".agents/skills/decide"
		}
	case "agents":
		config, skill = ".agents/mcp.json", ".agents/skills/decide"
	default:
		return Paths{}, errors.New("client must be opencode, claude, cursor, copilot, codex, devin, windsurf, antigravity, or agents")
	}
	paths := Paths{Skill: filepath.Join(base, filepath.FromSlash(skill), "SKILL.md"), ServerPath: server, TOML: client == "codex"}
	if config != "" {
		paths.MCP = filepath.Join(base, filepath.FromSlash(config))
	}
	return paths, nil
}

type Options struct {
	Client, Home, Project, Binary string
	Args                          []string
	MCP, Skill                    bool
	Log                           io.Writer
	// Disables the native OpenCode CLI for deterministic tests and offline hosts.
	Direct bool
}

func Install(ctx context.Context, o Options) (Paths, error) {
	paths, err := Locations(o.Client, o.Home, o.Project)
	if err != nil {
		return paths, err
	}
	if o.MCP && paths.MCP == "" {
		return paths, errors.New("legacy Windsurf/Cascade has no documented project MCP config; use devin for current project integration or omit --project for legacy Windsurf")
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.MCP {
		cli, lookupErr := exec.LookPath("opencode")
		if o.Client == "opencode" && !o.Direct && lookupErr == nil {
			args := []string{"mcp", "add", "decide"}
			if o.Project == "" {
				args = append(args, "--global")
			}
			args = append(args, "--", o.Binary)
			args = append(args, o.Args...)
			cmd := exec.CommandContext(ctx, cli, args...)
			cmd.Dir, cmd.Stdout, cmd.Stderr = o.Project, o.Log, o.Log
			if err = cmd.Run(); err != nil {
				return paths, err
			}
		} else {
			entry := map[string]any{"command": o.Binary, "args": o.Args, "type": "stdio"}
			if o.Client == "opencode" {
				entry = map[string]any{"type": "local", "command": append([]string{o.Binary}, o.Args...)}
			}
			if paths.TOML {
				delete(entry, "type")
			}
			if paths.MCP, err = mergeMCP(paths, entry); err != nil {
				return paths, err
			}
		}
	}
	if o.Skill {
		unlock, err := store.Lock(paths.Skill)
		if err != nil {
			return paths, err
		}
		defer unlock()
		old, err := os.ReadFile(paths.Skill)
		if err != nil && !os.IsNotExist(err) {
			return paths, err
		}
		if err == nil && !bytes.Contains(old, []byte("<!-- Managed by decide -->")) {
			return paths, fmt.Errorf("refusing to overwrite unmanaged skill: %s", paths.Skill)
		}
		if err = store.Write(paths.Skill, skills.Decide); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

func mergeMCP(paths Paths, entry map[string]any) (string, error) {
	path := paths.MCP
	if !paths.TOML {
		if _, err := os.Stat(path + "c"); err == nil {
			path += "c"
		}
	}
	unlock, err := store.Lock(path)
	if err != nil {
		return path, err
	}
	defer unlock()
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, err
	}
	var content []byte
	if paths.TOML {
		root := map[string]any{}
		if len(original) > 0 {
			if err = toml.Unmarshal(original, &root); err != nil {
				return path, err
			}
		}
		servers, ok := root["mcp_servers"].(map[string]any)
		if !ok {
			if _, exists := root["mcp_servers"]; exists {
				return path, errors.New("mcp_servers is not a table")
			}
			servers = map[string]any{}
			root["mcp_servers"] = servers
		}
		servers["decide"] = entry
		content, err = toml.Marshal(root)
		if err != nil {
			return path, err
		}
	} else {
		input := bytes.Clone(original)
		if len(input) == 0 {
			input = []byte("{}")
		}
		value, err := hujson.Parse(input)
		if err != nil {
			return path, err
		}
		pointer := ""
		// Check the root and each existing ancestor before adding the decide entry.
		for _, part := range append(append([]string{}, paths.ServerPath...), "decide") {
			parent := value.Find(pointer)
			if parent == nil {
				return path, errors.New("missing MCP configuration parent")
			}
			if _, ok := parent.Value.(*hujson.Object); !ok {
				return path, errors.New("MCP configuration parent must be an object")
			}
			pointer += "/" + part
			var add any = map[string]any{}
			if part == "decide" {
				add = entry
			} else if value.Find(pointer) != nil {
				continue
			}
			patch, _ := json.Marshal([]map[string]any{{"op": "add", "path": pointer, "value": add}})
			if err = value.Patch(patch); err != nil {
				return path, err
			}
		}
		if strings.HasSuffix(paths.MCP, "opencode.json") && value.Find("/$schema") == nil {
			_ = value.Patch([]byte(`[{"op":"add","path":"/$schema","value":"https://opencode.ai/config.json"}]`))
		}
		value.Format()
		content = value.Pack()
	}
	if len(original) > 0 && !bytes.Equal(original, content) {
		backup := path + ".decide-backup"
		if _, err = os.Stat(backup); os.IsNotExist(err) {
			if err = store.Write(backup, original); err != nil {
				return path, err
			}
		} else if err != nil {
			return path, err
		}
	}
	return path, store.Write(path, content)
}
