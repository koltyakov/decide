// Package decision turns provider answers into workflow results and explicit gates.
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"unicode/utf8"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
)

// MaxPolicies bounds the number of gates evaluated for one response.
const MaxPolicies = 64

// Keys is one or more declared Choice keys. A single key encodes as a string.
type Keys []string

func (k *Keys) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*k = Keys{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil || many == nil {
		return errors.New("is must be a string or an array of strings")
	}
	*k = many
	return nil
}

func (k Keys) MarshalJSON() ([]byte, error) {
	if len(k) == 1 {
		return json.Marshal(k[0])
	}
	return json.Marshal([]string(k))
}

type Policy struct {
	Answer        string   `json:"answer,omitempty"`
	AtLeast       *float64 `json:"atLeast,omitempty"`
	AtMost        *float64 `json:"atMost,omitempty"`
	Is            Keys     `json:"is,omitempty"`
	MinConfidence *float64 `json:"minConfidence,omitempty"`
}

func (p Policy) Active() bool {
	return p.AtLeast != nil || p.AtMost != nil || p.Is != nil || p.MinConfidence != nil
}

// Policies are independent gates that must all pass. One policy keeps the
// original _decide.policy/_decide.gate metadata shape.
type Policies []Policy

func (ps Policies) Active() bool {
	for _, p := range ps {
		if p.Active() {
			return true
		}
	}
	return false
}

// ParsePolicies strictly decodes a policy object or an array of policy objects.
// Unknown members are rejected so a misspelled rule can't silently disable a gate.
func ParsePolicies(b []byte) (Policies, error) {
	invalid := fault.New(fault.Usage, "policy must be an object, or an array of objects, with answer, atLeast, atMost, is, and minConfidence")
	b = bytes.TrimSpace(b)
	if !utf8.Valid(b) || len(b) == 0 {
		return nil, invalid
	}
	decode := func(target any) error {
		// encoding/json matches names case-insensitively, so check exact member
		// names first: "atleast" must fail, not become atLeast.
		var generic any
		d := json.NewDecoder(bytes.NewReader(b))
		if err := d.Decode(&generic); err != nil || d.Decode(new(any)) != io.EOF {
			return invalid
		}
		objects, isList := generic.([]any)
		if !isList {
			objects = []any{generic}
		}
		for _, value := range objects {
			object, ok := value.(map[string]any)
			if !ok {
				return invalid
			}
			for key, value := range object {
				switch key {
				case "answer", "atLeast", "atMost", "is", "minConfidence":
					if value == nil {
						return fault.New(fault.Usage, "policy member "+key+" cannot be null")
					}
				default:
					return invalid
				}
			}
		}
		if json.Unmarshal(b, target) != nil {
			return invalid
		}
		return nil
	}
	switch b[0] {
	case '{':
		var p Policy
		if err := decode(&p); err != nil {
			return nil, err
		}
		return Policies{p}, nil
	case '[':
		var ps Policies
		if err := decode(&ps); err != nil {
			return nil, err
		}
		return ps, nil
	}
	return nil, invalid
}

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

func (ps Policies) Validate(questions jev.Object) error {
	if len(ps) > MaxPolicies {
		return fault.New(fault.Usage, "at most 64 policies can gate one request")
	}
	for _, p := range ps {
		if len(ps) > 1 && !p.Active() {
			return fault.New(fault.Usage, "each policy in a list needs at least one rule")
		}
		if err := p.Validate(questions); err != nil {
			return err
		}
	}
	return nil
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
	for _, bound := range []struct {
		name  string
		value *float64
	}{{"--at-least", p.AtLeast}, {"--at-most", p.AtMost}} {
		if bound.value == nil {
			continue
		}
		max := 1.0
		switch q["type"] {
		case "noul":
		case "score":
			max = float64(len(q["criteria"].([]any)) - 1)
		default:
			return bad(bound.name + " applies only to noul and score answers")
		}
		if !finite(*bound.value) || *bound.value < 0 || *bound.value > max {
			return bad(bound.name + " must be within the answer's probability or score range")
		}
	}
	if p.AtLeast != nil && p.AtMost != nil && *p.AtLeast > *p.AtMost {
		return bad("--at-least must not exceed --at-most")
	}
	if p.Is != nil {
		if q["type"] != "choice" {
			return bad("--is applies only to choice answers")
		}
		if len(p.Is) == 0 {
			return bad("--is must name at least one declared classification option")
		}
		for _, key := range p.Is {
			if _, ok := q["criteria"].(map[string]any)[key]; !ok {
				return bad("--is must name a declared classification option")
			}
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
	Gates    []Gate
}

func (r Evaluation) Rejected() bool {
	for _, gate := range r.Gates {
		if !gate.Passed {
			return true
		}
	}
	return false
}

// Mode selects execution or an offline preview.
type Mode int

const (
	Execute Mode = iota
	// Preview validates and encodes without a key or network, showing the full payload.
	Preview
	// PreviewSummary is Preview with inline image bytes replaced by their size.
	PreviewSummary
)

// Evaluate is the single CLI/MCP workflow, including offline preflight and gating.
func Evaluate(ctx context.Context, client jev.Client, request jev.Object, options jev.Options, policies Policies, mode Mode) (*Evaluation, error) {
	options, err := client.Resolve(options)
	if err != nil {
		return nil, err
	}
	prepared, err := jev.Prepare(request, options)
	if err != nil {
		return nil, err
	}
	return Run(ctx, client, prepared, policies, mode)
}

// Run validates policies, then previews or executes a prepared request once.
func Run(ctx context.Context, client jev.Client, prepared *jev.Prepared, policies Policies, mode Mode) (*Evaluation, error) {
	if err := policies.Validate(prepared.Questions); err != nil {
		return nil, err
	}
	if mode != Execute {
		preview := prepared.Preview()
		if mode == PreviewSummary {
			preview = prepared.Summary()
		}
		if len(policies) > 1 {
			preview["policies"] = policies
		} else {
			preview["policy"] = Policy{}
			if len(policies) == 1 {
				preview["policy"] = policies[0]
			}
		}
		return &Evaluation{Response: preview}, nil
	}
	raw, err := client.Execute(ctx, prepared)
	if err != nil {
		return nil, err
	}
	r := &Evaluation{Raw: raw}
	answers := raw["answers"].(map[string]any)
	output := maps.Clone(raw)
	var active Policies
	for _, policy := range policies {
		if policy.Active() {
			active = append(active, policy)
		}
	}
	for _, policy := range active {
		name, spec, _ := Select(prepared.Questions, policy.Answer)
		r.Gates = append(r.Gates, policy.gate(name, answers[name].(map[string]any), spec["type"].(string)))
	}
	switch {
	case len(active) == 1:
		output["_decide"] = jev.Object{"gate": r.Gates[0], "policy": active[0]}
	case len(active) > 1:
		output["_decide"] = jev.Object{"gates": r.Gates, "policies": active, "passed": !r.Rejected()}
	}
	r.Response = output
	return r, nil
}

func (p Policy) gate(name string, answer jev.Object, kind string) Gate {
	value := answer[kind]
	gate := Gate{Answer: name, Passed: true, Checks: []Check{}}
	add := func(rule string, passed bool, reason string) {
		gate.Checks = append(gate.Checks, Check{Rule: rule, Passed: passed, Reason: reason})
		gate.Passed = gate.Passed && passed
	}
	if p.AtLeast != nil {
		n, ok := number(value)
		add("atLeast", ok && n >= *p.AtLeast, "")
	}
	if p.AtMost != nil {
		n, ok := number(value)
		add("atMost", ok && n <= *p.AtMost, "")
	}
	if p.Is != nil {
		choice, _ := value.(string)
		matched := false
		for _, key := range p.Is {
			matched = matched || choice == key
		}
		add("is", matched, "")
	}
	if p.MinConfidence != nil {
		n, ok := number(answer["confidence"])
		reason := ""
		if !ok || n < 0 || n > 1 {
			reason = "confidence_unavailable"
			ok = false
		}
		add("minConfidence", ok && n >= *p.MinConfidence, reason)
	}
	return gate
}

func number(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	v, err := n.Float64()
	return v, err == nil && finite(v)
}
