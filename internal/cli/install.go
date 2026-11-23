package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/installation"
	"github.com/spf13/cobra"
)

// installSelf copies the running executable to the per-user install location
// shared with the release installers.
func (a *app) installSelf() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use: "install-self", Short: "Install this executable to the per-user location used by the release installers", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			source, err := os.Executable()
			if err != nil {
				return err
			}
			dest, err := installation.BinaryPath()
			if err != nil {
				return err
			}
			result, err := installation.InstallSelf(cmd.Context(), source, dest, installation.MainPackage)
			if err != nil {
				return err
			}
			output := map[string]any{"ok": true, "path": result.Path, "changed": result.Changed, "path_added": result.PathAdded, "on_path": result.OnPath}
			if refresh {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				refreshed, err := installation.RefreshSkills(home, "")
				if err != nil {
					return err
				}
				output["refreshed"] = refreshed
			}
			return a.print(output)
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh-skills", false, "also update existing global decide-managed skills; never creates new ones")
	return cmd
}

func (a *app) install(command string) *cobra.Command {
	var project string
	var refresh bool
	cmd := &cobra.Command{
		Use: command + " CLIENT", Short: "Install agent integration globally or with --project DIR",
		Args: func(_ *cobra.Command, args []string) error {
			if refresh {
				if len(args) != 0 {
					return fault.New(fault.Usage, "--refresh updates every client's managed skill; omit CLIENT")
				}
				return nil
			}
			if len(args) != 1 {
				return fault.New(fault.Usage, "provide a client: "+strings.Join(installation.Clients, ", "))
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
			if refresh {
				refreshed, err := installation.RefreshSkills(home, project)
				if err != nil {
					return err
				}
				return a.print(map[string]any{"ok": true, "refreshed": refreshed})
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
	if command == "install-skill" {
		cmd.Use = command + " CLIENT | --refresh"
		cmd.Flags().BoolVar(&refresh, "refresh", false, "update existing decide-managed skills for every client after an upgrade; never creates new ones")
	}
	return cmd
}
