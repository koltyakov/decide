package cli

import (
	"fmt"

	"github.com/koltyakov/decide/internal/decision"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
)

type executionFlags struct {
	output              string
	dryRun              bool
	answer, is          string
	atLeast, confidence float64
}

func (f *executionFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.output, "output", "json", "json for workflow results, value for one answer, raw for the API response")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "validate and show the request without credentials or an API call")
	cmd.Flags().StringVar(&f.answer, "answer", "", "named question to select for value output and gates; optional for a single question")
	cmd.Flags().Float64Var(&f.atLeast, "at-least", 0, "accept only when the probability or score reaches this threshold")
	cmd.Flags().StringVar(&f.is, "is", "", "accept only this declared classification key")
	cmd.Flags().Float64Var(&f.confidence, "min-confidence", 0, "require this confidence; missing confidence fails the gate")
}

func (f executionFlags) policy(cmd *cobra.Command) decision.Policy {
	p := decision.Policy{Answer: f.answer}
	if cmd.Flags().Changed("at-least") {
		p.AtLeast = &f.atLeast
	}
	if cmd.Flags().Changed("is") {
		p.Is = &f.is
	}
	if cmd.Flags().Changed("min-confidence") {
		p.MinConfidence = &f.confidence
	}
	return p
}

func (f executionFlags) validate(p decision.Policy, questions jev.Object) error {
	if f.output != "json" && f.output != "raw" && f.output != "value" {
		return fault.New(fault.Usage, "--output must be json, value, or raw")
	}
	if f.dryRun && f.output != "json" {
		return fault.New(fault.Usage, "--dry-run requires JSON output")
	}
	if f.output == "raw" && p.Active() {
		return fault.New(fault.Usage, "raw output cannot include policy gates; use json or value")
	}
	if err := p.Validate(questions); err != nil {
		return err
	}
	if f.output == "value" {
		_, _, err := decision.Select(questions, p.Answer)
		return err
	}
	return nil
}

func (a *app) execute(cmd *cobra.Command, request jev.Object, options jev.Options, f executionFlags) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	p := f.policy(cmd)
	// Validate output selection before any billable work.
	data := request
	if err = jev.Validate(data); err != nil {
		return err
	}
	if err = f.validate(p, data["questions"].(map[string]any)); err != nil {
		return err
	}
	result, err := decision.Evaluate(cmd.Context(), client, request, options, p, f.dryRun)
	if err != nil {
		return err
	}
	switch f.output {
	case "raw":
		err = a.print(result.Raw)
	case "value":
		name, _, _ := decision.Select(data["questions"].(map[string]any), p.Answer)
		kind := data["questions"].(map[string]any)[name].(map[string]any)["type"].(string)
		value := result.Raw["answers"].(map[string]any)[name].(map[string]any)[kind]
		if _, ok := value.(map[string]any); ok {
			err = a.print(value)
		} else {
			_, err = fmt.Fprintln(a.out, value)
		}
	default:
		err = a.print(result.Response)
	}
	if err != nil {
		return err
	}
	if result.Rejected() {
		return fault.New(fault.Rejected, "decision gate not met")
	}
	return nil
}
