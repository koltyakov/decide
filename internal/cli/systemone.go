package cli

import (
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
)

func (a *app) recipe(path string) (jev.Object, error) {
	b, err := a.readFile(path)
	if err != nil {
		return nil, err
	}
	var recipe jev.Object
	if err = jev.Decode(b, &recipe); err != nil {
		return nil, err
	}
	if recipe == nil {
		return nil, fault.New(fault.Usage, "recipe must be a JSON object with named questions")
	}
	return recipe, nil
}

func (a *app) systemOne() *cobra.Command {
	var input inputFlags
	var execution executionFlags
	var images []string
	cmd := &cobra.Command{Use: "systemone REQUEST|-", Short: "Call System One with a native request, optionally replacing state", Args: oneArgument("systemone")}
	input.bind(cmd)
	execution.bind(cmd)
	cmd.Flags().StringArrayVar(&images, "image", nil, "REFERENCE=PATH or REFERENCE=-, matching a reference in the recipe")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := input.validate(cmd); err != nil {
			return err
		}
		stdinState := input.explicit(cmd) && !cmd.Flags().Changed("state") && (input.path == "" || input.path == "-")
		if (args[0] == "-" && (stdinState || imageUsesStdin(images))) || (stdinState && imageUsesStdin(images)) {
			return fault.New(fault.Usage, "stdin can supply only one of recipe, state, or image")
		}
		request, err := a.recipe(args[0])
		if err != nil {
			return err
		}
		if input.explicit(cmd) {
			request["state"], err = input.read(a, cmd)
			if err != nil {
				return err
			}
		}
		attachments, _, err := a.images(images, false)
		if err != nil {
			return err
		}
		o := a.options
		o.Attachments = attachments
		return a.execute(cmd, request, o, execution)
	}
	return cmd
}
