package cli

import (
	"strings"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
)

func oneArgument(name string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return fault.New(fault.Usage, name+" requires exactly one argument")
		}
		return nil
	}
}

func (a *app) decision(kind string) *cobra.Command {
	var input inputFlags
	var execution executionFlags
	var instructions, instructionsFile, instructionsFormat, criteria, criteriaFile string
	var images []string
	short := map[string]string{"noul": "Evaluate a yes/no statement as a probability", "choice": "Select one key from criteria", "score": "Score against ordered criteria", "box": "Locate a target in an image, experimental"}[kind]
	cmd := &cobra.Command{Use: kind, Short: short, Args: noArgs}
	input.bind(cmd)
	execution.bind(cmd)
	cmd.Flags().StringVar(&instructions, "instructions", "", "the question or instructions, using Jev's native field name")
	cmd.Flags().StringVar(&instructionsFile, "instructions-file", "", "read instructions from a file or - for stdin")
	cmd.Flags().StringVar(&instructionsFormat, "instructions-format", "text", "instructions encoding: text or json")
	cmd.Flags().StringVar(&criteria, "criteria", "", "native JSON criteria: choice object, score array, or optional noul true/false object")
	cmd.Flags().StringVar(&criteriaFile, "criteria-file", "", "read native JSON criteria from a file")
	cmd.Flags().StringArrayVar(&images, "image", nil, "NAME=PATH or NAME=- for a PNG/JPEG; repeat for multiple images")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := input.validate(cmd); err != nil {
			return err
		}
		if instructionsFormat != "text" && instructionsFormat != "json" {
			return fault.New(fault.Usage, "--instructions-format must be text or json")
		}
		if cmd.Flags().Changed("instructions") && cmd.Flags().Changed("instructions-file") {
			return fault.New(fault.Usage, "choose --instructions or --instructions-file")
		}
		if cmd.Flags().Changed("criteria") && cmd.Flags().Changed("criteria-file") {
			return fault.New(fault.Usage, "choose --criteria or --criteria-file")
		}
		if !cmd.Flags().Changed("instructions") && !cmd.Flags().Changed("instructions-file") && !cmd.Flags().Changed("criteria") && !cmd.Flags().Changed("criteria-file") {
			return fault.New(fault.Usage, "provide --instructions or --criteria to define the question")
		}
		// Count stdin consumers before reading any stream.
		stateStdin := !cmd.Flags().Changed("state") && (input.path == "" || input.path == "-") && (input.explicit(cmd) || len(images) == 0)
		consumers := 0
		for _, used := range []bool{stateStdin, imageUsesStdin(images), cmd.Flags().Changed("instructions-file") && instructionsFile == "-", cmd.Flags().Changed("criteria-file") && criteriaFile == "-"} {
			if used {
				consumers++
			}
		}
		if consumers > 1 {
			return fault.New(fault.Usage, "stdin can supply only one of state, instructions, criteria, or image")
		}
		b := []byte(instructions)
		var err error
		if cmd.Flags().Changed("instructions-file") {
			b, err = a.readFile(instructionsFile)
			if err != nil {
				return err
			}
		}
		instruction, err := decodeState(b, instructionsFormat)
		if err != nil {
			return err
		}
		request := jev.Question(kind, "", "", nil)
		q := request["questions"].(map[string]any)["answer"].(map[string]any)
		q["instructions"] = instruction
		if cmd.Flags().Changed("criteria") || cmd.Flags().Changed("criteria-file") {
			b = []byte(criteria)
			if cmd.Flags().Changed("criteria-file") {
				b, err = a.readFile(criteriaFile)
				if err != nil {
					return err
				}
			}
			var value any
			if err = jev.Decode(b, &value); err != nil {
				return err
			}
			q["criteria"] = value
		}
		if err = jev.Validate(request); err != nil {
			return err
		}
		if err = execution.validate(execution.policy(cmd), request["questions"].(map[string]any)); err != nil {
			return err
		}
		attachments, imageState, err := a.images(images, true)
		if err != nil {
			return err
		}
		var state any
		if len(images) == 0 || input.explicit(cmd) {
			state, err = input.read(a, cmd)
			if err != nil {
				return err
			}
		}
		if len(images) > 0 {
			if input.explicit(cmd) {
				state = jev.Object{"context": state, "images": imageState}
			} else {
				state = imageState
			}
		}
		request["state"] = state
		o := a.options
		o.Attachments = attachments
		return a.execute(cmd, request, o, execution)
	}
	return cmd
}
