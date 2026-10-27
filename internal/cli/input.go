package cli

import (
	"io"
	"os"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type inputFlags struct{ state, path, format string }

func (i *inputFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&i.state, "state", "", "literal state; never interpreted as a file path")
	cmd.Flags().StringVar(&i.path, "state-file", "", "state file or - for stdin; omitted reads stdin unless images supply the state")
	cmd.Flags().StringVar(&i.format, "state-format", "text", "state encoding: text or json")
}

func (i inputFlags) explicit(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("state") || cmd.Flags().Changed("state-file")
}

func (a *app) open(path string) (io.ReadCloser, error) {
	if path == "" || path == "-" {
		if f, ok := a.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			return nil, fault.New(fault.Usage, "provide --state, --state-file, --image, or piped stdin")
		}
		if closer, ok := a.in.(io.ReadCloser); ok {
			return closer, nil
		}
		return io.NopCloser(a.in), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fault.New(fault.Usage, "cannot open input file")
	}
	return f, nil
}

func (a *app) readFile(path string) ([]byte, error) {
	r, err := a.open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return jev.Read(r)
}

func (i inputFlags) validate(cmd *cobra.Command) error {
	if i.format != "text" && i.format != "json" {
		return fault.New(fault.Usage, "--state-format must be text or json")
	}
	if cmd.Flags().Changed("state") && cmd.Flags().Changed("state-file") {
		return fault.New(fault.Usage, "--state and --state-file are mutually exclusive")
	}
	return nil
}

func (i inputFlags) read(a *app, cmd *cobra.Command) (any, error) {
	if err := i.validate(cmd); err != nil {
		return nil, err
	}
	var b []byte
	var err error
	if cmd.Flags().Changed("state") {
		b = []byte(i.state)
	} else {
		b, err = a.readFile(i.path)
	}
	if err != nil {
		return nil, err
	}
	if len(b) > jev.MaxBytes {
		return nil, fault.New(fault.Usage, "state exceeds 32 MiB")
	}
	return decodeState(b, i.format)
}

func decodeState(b []byte, format string) (any, error) {
	if format == "json" {
		var value any
		if err := jev.Decode(b, &value); err != nil {
			return nil, err
		}
		return value, nil
	}
	return string(b), nil
}
