// Package cli defines the decide command-line interface.
package cli

import (
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/koltyakov/decide/internal/agent"
	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

type app struct {
	version, credentialPath string
	options                 jev.Options
	timeout                 time.Duration
	pretty                  bool
	in                      io.Reader
	out, diagnostic         io.Writer
}

func New(version string, in io.Reader, out, diagnostic io.Writer) *cobra.Command {
	a := &app{version: version, in: in, out: out, diagnostic: diagnostic}
	root := &cobra.Command{
		Use: "decide", Short: "Typed model decisions for agent workflows", Version: version,
		SilenceUsage: true, SilenceErrors: true,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if a.timeout <= 0 {
				return fault.New(fault.Usage, "timeout must be positive")
			}
			return nil
		},
	}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(diagnostic)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return fault.New(fault.Usage, err.Error()) })
	root.PersistentFlags().StringVar(&a.options.Provider, "provider", "", "API provider; overrides DECIDE_PROVIDER, saved default, then official")
	root.PersistentFlags().StringVar(&a.options.Model, "model", "", "override provider or request model")
	root.PersistentFlags().StringVar(&a.options.Endpoint, "endpoint", "", "trusted HTTPS endpoint; HTTP allowed only on loopback")
	root.PersistentFlags().StringVar(&a.credentialPath, "credentials", "", "credential file; default DECIDE_CREDENTIALS_FILE or XDG config path")
	root.PersistentFlags().DurationVar(&a.timeout, "timeout", time.Minute, "API request timeout")
	root.PersistentFlags().BoolVar(&a.pretty, "pretty", false, "indent JSON output")
	root.PersistentFlags().BoolVar(&a.options.Images, "experimental-images", false, "opt in to experimental TypeSafe images and box questions")
	root.PersistentFlags().BoolVar(&a.options.LongContext, "long-context", false, "opt in to TypeSafe experimental long context")
	root.PersistentFlags().StringVar(&a.options.Compression, "compression", "none", "request compression: none, gzip, x-gzip, or zstd; official/custom only")
	for _, kind := range []string{"noul", "choice", "score", "box"} {
		root.AddCommand(a.decision(kind))
	}
	root.AddCommand(a.systemOne(), a.batch(), a.providers(), a.install("install-mcp"), a.install("install-skill"))
	root.AddCommand(&cobra.Command{
		Use: "models", Short: "List the selected provider's native model catalog", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.Models(cmd.Context(), a.options)
			if err != nil {
				return err
			}
			return a.print(result)
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "capabilities", Short: "Inspect effective provider/model capabilities and process permissions offline", Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			result, err := client.Describe(a.options)
			if err != nil {
				return err
			}
			return a.print(result)
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "mcp", Short: "Serve decision tools over MCP stdio", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			reader := io.NopCloser(a.in)
			if closer, ok := a.in.(io.ReadCloser); ok {
				reader = closer
			}
			return agent.Server(client, a.options, a.version).Run(cmd.Context(), &mcp.IOTransport{
				Reader: reader, Writer: nopWriteCloser{a.out}, MaxLineLength: jev.MaxBytes + 1<<20,
			})
		},
	})
	root.CompletionOptions.DisableDefaultCmd = true
	return root
}

// Argument validation uses the same usage exit code as flag validation.
func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return fault.New(fault.Usage, err.Error())
	}
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func (a *app) path() (string, error) {
	if a.credentialPath != "" {
		return a.credentialPath, nil
	}
	if value := os.Getenv("DECIDE_CREDENTIALS_FILE"); value != "" {
		return value, nil
	}
	return credentials.DefaultPath()
}

func (a *app) client() (jev.Client, error) {
	path, err := a.path()
	return jev.Client{Credentials: credentials.Store{Path: path}, Timeout: a.timeout, Version: a.version}, err
}

func (a *app) print(value any) error {
	e := json.NewEncoder(a.out)
	e.SetEscapeHTML(false)
	if a.pretty {
		e.SetIndent("", "  ")
	}
	return e.Encode(value)
}
