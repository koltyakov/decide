// Package decision turns provider answers into workflow results and explicit gates.
package decision

import (
	"context"
	"encoding/json"
	"math"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
)

type Policy struct {
	Answer        string   `json:"answer,omitempty"`
	AtLeast       *float64 `json:"atLeast,omitempty"`
	Is            *string  `json:"is,omitempty"`
	MinConfidence *float64 `json:"minConfidence,omitempty"`
}

func (p Policy) Active() bool { return p.AtLeast != nil || p.Is != nil || p.MinConfidence != nil }

// Select never guesses which answer matters in a multi-question recipe.
func Select(questions jev.Object, name string) (string, jev.Object, error) {
	if name == "" && len(questions) == 1 {
		for key := range questions {
			name = key
		}
	}
	q, ok := questions[name].(map[string]any)
	if !ok {
		return "", nil, fault.New(fault.Usage, "select a question with --answer; multi-question recipes have no implicit primary answer")
	}
	return name, q, nil
}

func (p Policy) Validate(questions jev.Object) error {
	if !p.Active() && p.Answer == "" {
		return nil
	}
	_, q, err := Select(questions, p.Answer)
	if err != nil {
		return err
	}
	bad := func(message string) error { return fault.New(fault.Usage, message) }
	if p.AtLeast != nil {
		max := 1.0
		switch q["type"] {
		case "noul":
		case "score":
			max = float64(len(q["criteria"].([]any)) - 1)
		default:
			return bad("--at-least applies only to noul and score answers")
		}
		if !finite(*p.AtLeast) || *p.AtLeast < 0 || *p.AtLeast > max {
			return bad("--at-least must be within the answer's probability or score range")
		}
	}
	if p.Is != nil {
		if q["type"] != "choice" {
			return bad("--is applies only to choice answers")
		}
		if _, ok := q["criteria"].(map[string]any)[*p.Is]; !ok {
			return bad("--is must name a declared classification option")
		}
	}
	if p.MinConfidence != nil {
		if q["type"] == "noul" {
			return bad("noul returns a probability, not confidence; use --at-least")
		}
		if !finite(*p.MinConfidence) || *p.MinConfidence < 0 || *p.MinConfidence > 1 {
			return bad("--min-confidence must be between 0 and 1")
		}
	}
	return nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

type Check struct {
	Rule   string `json:"rule"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

type Gate struct {
	Answer string  `json:"answer"`
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

type Evaluation struct {
	Response any
	Raw      jev.Object
	Gate     *Gate
}

func (r Evaluation) Rejected() bool { return r.Gate != nil && !r.Gate.Passed }

// Evaluate is the single CLI/MCP workflow, including offline preflight and gating.
func Evaluate(ctx context.Context, client jev.Client, request jev.Object, options jev.Options, policy Policy, dryRun bool) (*Evaluation, error) {
	options, err := client.Resolve(options)
	if err != nil {
		return nil, err
	}
	prepared, err := jev.Prepare(request, options)
	if err != nil {
		return nil, err
	}
	if err = policy.Validate(prepared.Questions); err != nil {
		return nil, err
	}
	if dryRun {
		preview := prepared.Preview()
		preview["policy"] = policy
		return &Evaluation{Response: preview}, nil
	}
	raw, err := client.Execute(ctx, prepared)
	if err != nil {
		return nil, err
	}
	r := &Evaluation{Raw: raw}
	answers := raw["answers"].(map[string]any)
	output := jev.Object{}
	for key, value := range raw {
		output[key] = value
	}
	if policy.Active() {
		name, spec, _ := Select(prepared.Questions, policy.Answer)
		answer := answers[name].(map[string]any)
		value := answer[spec["type"].(string)]
		gate := &Gate{Answer: name, Passed: true, Checks: []Check{}}
		add := func(rule string, passed bool, reason string) {
			gate.Checks = append(gate.Checks, Check{Rule: rule, Passed: passed, Reason: reason})
			gate.Passed = gate.Passed && passed
		}
		if policy.AtLeast != nil {
			n, ok := number(value)
			add("atLeast", ok && n >= *policy.AtLeast, "")
		}
		if policy.Is != nil {
			add("is", value == *policy.Is, "")
		}
		if policy.MinConfidence != nil {
			n, ok := number(answer["confidence"])
			reason := ""
			if !ok || n < 0 || n > 1 {
				reason = "confidence_unavailable"
				ok = false
			}
			add("minConfidence", ok && n >= *policy.MinConfidence, reason)
		}
		r.Gate = gate
		output["_decide"] = jev.Object{"gate": gate, "policy": policy}
	}
	r.Response = output
	return r, nil
}

func number(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Float64()
	return v, err == nil && finite(v)
}
