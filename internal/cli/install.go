package cli

import (
	"os"
	"path/filepath"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/installation"
	"github.com/spf13/cobra"
)

func (a *app) install(command string) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use: command + " CLIENT", Short: "Install agent integration globally or with --project DIR",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fault.New(fault.Usage, "provide a client: opencode, claude, cursor, copilot, codex, devin, windsurf, antigravity, or agents")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			if project != "" {
				project, err = filepath.Abs(project)
				if err != nil {
					return err
				}
				if info, err := os.Stat(project); err != nil || !info.IsDir() {
					return fault.New(fault.Usage, "project must be an existing directory")
				}
			}
			binary, err := os.Executable()
			if err != nil {
				return err
			}
			path, err := a.path()
			if err != nil {
				return err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}
			mcpArgs := []string{"--credentials", path}
			for _, flag := range []struct{ name, value string }{{"provider", a.options.Provider}, {"model", a.options.Model}, {"endpoint", a.options.Endpoint}} {
				if flag.value != "" {
					mcpArgs = append(mcpArgs, "--"+flag.name, flag.value)
				}
			}
			mcpArgs = append(mcpArgs, "--timeout", a.timeout.String(), "mcp")
			if a.options.Images {
				mcpArgs = append(mcpArgs, "--experimental-images")
			}
			if a.options.LongContext {
				mcpArgs = append(mcpArgs, "--long-context")
			}
			if a.options.Compression != "none" && a.options.Compression != "" {
				mcpArgs = append(mcpArgs, "--compression", a.options.Compression)
			}
			paths, err := installation.Install(cmd.Context(), installation.Options{
				Client: args[0], Home: home, Project: project, Binary: binary, Args: mcpArgs,
				MCP: command == "install-mcp", Skill: command == "install-skill", Log: a.diagnostic,
			})
			if err != nil {
				return err
			}
			destination := paths.Skill
			if command == "install-mcp" {
				destination = paths.MCP
			}
			return a.print(map[string]any{"ok": true, "client": args[0], "path": destination})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project directory; omitted installs globally")
	return cmd
}
