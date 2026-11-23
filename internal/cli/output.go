package cli

import (
	"fmt"

	"github.com/koltyakov/decide/internal/decision"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
)

type executionFlags struct {
	output, policy, previewImages string
	dryRun                        bool
	answer                        string
	is                            []string
	atLeast, atMost, confidence   float64
}

var ruleFlags = []string{"at-least", "at-most", "is", "min-confidence"}

func (f *executionFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.output, "output", "json", "json for workflow results, value for one answer, raw for the API response")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "validate and show the request without credentials or an API call")
	cmd.Flags().StringVar(&f.previewImages, "preview-images", "full", "dry-run image data: full, or summary to replace inline bytes with their size")
	cmd.Flags().StringVar(&f.answer, "answer", "", "named question to select for value output and gates; optional for a single question")
	cmd.Flags().Float64Var(&f.atLeast, "at-least", 0, "accept only when the probability or score reaches this threshold")
	cmd.Flags().Float64Var(&f.atMost, "at-most", 0, "accept only when the probability or score does not exceed this threshold")
	cmd.Flags().StringArrayVar(&f.is, "is", nil, "accept only this declared classification key; repeat to accept any of several")
	cmd.Flags().Float64Var(&f.confidence, "min-confidence", 0, "require this confidence; missing confidence fails the gate")
	cmd.Flags().StringVar(&f.policy, "policy", "", "literal JSON policy object or array of policies, all of which must pass; replaces the rule flags")
}

func (f executionFlags) policies(cmd *cobra.Command) (decision.Policies, error) {
	if cmd.Flags().Changed("policy") {
		for _, name := range ruleFlags {
			if cmd.Flags().Changed(name) {
				return nil, fault.New(fault.Usage, "--policy replaces --at-least, --at-most, --is, and --min-confidence")
			}
		}
		return decision.ParsePolicies([]byte(f.policy))
	}
	p := decision.Policy{Answer: f.answer}
	if cmd.Flags().Changed("at-least") {
		p.AtLeast = &f.atLeast
	}
	if cmd.Flags().Changed("at-most") {
		p.AtMost = &f.atMost
	}
	if cmd.Flags().Changed("is") {
		p.Is = decision.Keys(f.is)
	}
	if cmd.Flags().Changed("min-confidence") {
		p.MinConfidence = &f.confidence
	}
	return decision.Policies{p}, nil
}

// selection names the answer for value output: --answer, else a single policy's answer.
func (f executionFlags) selection(policies decision.Policies) string {
	if f.answer == "" && len(policies) == 1 {
		return policies[0].Answer
	}
	return f.answer
}

func (f executionFlags) mode() decision.Mode {
	switch {
	case !f.dryRun:
		return decision.Execute
	case f.previewImages == "summary":
		return decision.PreviewSummary
	}
	return decision.Preview
}

func (f executionFlags) validate(cmd *cobra.Command, policies decision.Policies, questions jev.Object) error {
	if f.output != "json" && f.output != "raw" && f.output != "value" {
		return fault.New(fault.Usage, "--output must be json, value, or raw")
	}
	if f.previewImages != "full" && f.previewImages != "summary" {
		return fault.New(fault.Usage, "--preview-images must be full or summary")
	}
	if cmd.Flags().Changed("preview-images") && !f.dryRun {
		return fault.New(fault.Usage, "--preview-images requires --dry-run")
	}
	if f.dryRun && f.output != "json" {
		return fault.New(fault.Usage, "--dry-run requires JSON output")
	}
	if f.output == "raw" && policies.Active() {
		return fault.New(fault.Usage, "raw output cannot include policy gates; use json or value")
	}
	if err := policies.Validate(questions); err != nil {
		return err
	}
	if f.output == "value" {
		_, _, err := decision.Select(questions, f.selection(policies))
		return err
	}
	return nil
}

func (a *app) execute(cmd *cobra.Command, request jev.Object, options jev.Options, f executionFlags) error {
	client, err := a.client()
	if err != nil {
		return err
	}
	policies, err := f.policies(cmd)
	if err != nil {
		return err
	}
	// Validate output selection before any billable work.
	if err = jev.Validate(request); err != nil {
		return err
	}
	questions := request["questions"].(map[string]any)
	if err = f.validate(cmd, policies, questions); err != nil {
		return err
	}
	result, err := decision.Evaluate(cmd.Context(), client, request, options, policies, f.mode())
	if err != nil {
		return err
	}
	switch f.output {
	case "raw":
		err = a.print(result.Raw)
	case "value":
		name, spec, _ := decision.Select(questions, f.selection(policies))
		value := result.Raw["answers"].(map[string]any)[name].(map[string]any)[spec["type"].(string)]
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
