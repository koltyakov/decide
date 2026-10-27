package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *app) providers() *cobra.Command {
	root := &cobra.Command{Use: "providers", Short: "Connect API providers, select a default, and inspect connection status"}
	root.AddCommand(&cobra.Command{Use: "list", Short: "List providers and key availability, without network calls", Args: noArgs, RunE: func(_ *cobra.Command, _ []string) error {
		client, err := a.client()
		if err != nil {
			return err
		}
		resolved, err := client.Resolve(a.options)
		if err != nil {
			return err
		}
		connections, err := client.Credentials.Connections()
		if err != nil {
			return err
		}
		rows := []jev.Object{}
		for _, name := range jev.ProviderNames() {
			provider, _ := jev.Lookup(name)
			_, source, keyErr := client.Credentials.Get(name, provider.KeyEnv)
			_, configured := connections.Providers[name]
			rows = append(rows, jev.Object{"provider": name, "family": provider.Family, "defaultModel": provider.Model, "capabilities": provider.CapabilitiesFor(provider.Model), "selected": name == resolved.Provider, "configured": configured, "keyAvailable": keyErr == nil, "keySource": source, "keyEnvironment": provider.KeyEnv})
		}
		return a.print(jev.Object{"providers": rows})
	}})
	for _, action := range []string{"status", "test", "disconnect"} {
		short := map[string]string{"status": "Inspect connection settings, capabilities, and key source, never the key", "test": "Verify the connection; official/DigitalOcean use model discovery, others a small billable request", "disconnect": "Remove saved settings and key; does not revoke remote credentials or unset environment variables"}[action]
		root.AddCommand(&cobra.Command{Use: action + " [PROVIDER]", Short: short, Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fault.New(fault.Usage, "provide at most one provider")
			}
			return nil
		}, RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client()
			if err != nil {
				return err
			}
			o := a.options
			if len(args) == 1 {
				o.Provider = args[0]
			}
			o, err = client.Resolve(o)
			if err != nil {
				return err
			}
			provider, _ := jev.Lookup(o.Provider)
			if action == "disconnect" {
				if err = client.Credentials.Disconnect(o.Provider); err != nil {
					return err
				}
				return a.print(jev.Object{"ok": true, "provider": o.Provider, "disconnected": true, "environmentOverride": os.Getenv(provider.KeyEnv) != ""})
			}
			if action == "test" {
				if err = a.testConnection(cmd, client, o); err != nil {
					return err
				}
				return a.print(jev.Object{"ok": true, "provider": o.Provider, "verified": true})
			}
			_, source, keyErr := client.Credentials.Get(o.Provider, provider.KeyEnv)
			settings, configErr := client.Configuration(o)
			status := jev.Object{"provider": o.Provider, "family": provider.Family, "capabilities": provider.CapabilitiesFor(settings.Model), "endpoint": settings.Endpoint, "model": settings.Model, "keyAvailable": keyErr == nil, "keySource": source, "keyEnvironment": provider.KeyEnv, "store": client.Credentials.Path, "ready": configErr == nil && keyErr == nil}
			if configErr != nil {
				status["configurationError"] = configErr.Error()
			}
			return a.print(status)
		}})
	}
	root.AddCommand(&cobra.Command{Use: "use PROVIDER", Short: "Save the default provider without sending a request", Args: oneArgument("providers use"), RunE: func(_ *cobra.Command, args []string) error {
		if _, err := jev.Lookup(args[0]); err != nil {
			return err
		}
		client, err := a.client()
		if err != nil {
			return err
		}
		if err = client.Credentials.Use(args[0]); err != nil {
			return err
		}
		return a.print(jev.Object{"ok": true, "defaultProvider": args[0], "environmentOverride": os.Getenv("DECIDE_PROVIDER") != ""})
	}})
	var keyStdin bool
	connect := &cobra.Command{Use: "connect PROVIDER", Short: "Validate a provider connection before saving its settings and key", Args: oneArgument("providers connect")}
	connect.Flags().BoolVar(&keyStdin, "key-stdin", false, "read a replacement API key from stdin, not the environment or saved store")
	connect.RunE = func(cmd *cobra.Command, args []string) error {
		provider, err := jev.Lookup(args[0])
		if err != nil {
			return err
		}
		client, err := a.client()
		if err != nil {
			return err
		}
		o := a.options
		o.Provider = args[0]
		o, err = client.Resolve(o)
		if err != nil {
			return err
		}
		key, source, keyErr := client.Credentials.Get(o.Provider, provider.KeyEnv)
		if keyStdin || keyErr != nil {
			var b []byte
			if f, ok := a.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) && !keyStdin {
				_, _ = fmt.Fprint(a.diagnostic, "API key: ")
				b, err = term.ReadPassword(int(f.Fd()))
				_, _ = fmt.Fprintln(a.diagnostic)
			} else {
				b, err = jev.Read(a.in)
			}
			if err != nil {
				return fault.New(fault.Usage, "could not read API key")
			}
			key = strings.TrimSpace(string(b))
			source = "input"
		}
		if key == "" || strings.ContainsAny(key, "\r\n") {
			return fault.New(fault.Usage, "API key must be a single non-empty line")
		}
		client.Key = key
		if err = a.testConnection(cmd, client, o); err != nil {
			return err
		}
		storedKey := ""
		if source != "environment" {
			storedKey = key
		}
		connection := credentials.Connection{Endpoint: o.Endpoint, Model: o.Model}
		if connection.Model == "" {
			connection.Model = o.DefaultModel
		}
		if err = client.Credentials.SaveConnection(o.Provider, storedKey, connection); err != nil {
			return err
		}
		return a.print(jev.Object{"ok": true, "provider": o.Provider, "verified": true, "keySource": source, "store": client.Credentials.Path})
	}
	root.AddCommand(connect)
	return root
}

func (a *app) testConnection(cmd *cobra.Command, client jev.Client, options jev.Options) error {
	if options.Provider == "official" || options.Provider == "digitalocean" {
		_, err := client.Models(cmd.Context(), options)
		return err
	}
	_, err := client.Evaluate(cmd.Context(), jev.Question("noul", "connection test", "Is this a connection test?", nil), options)
	return err
}
