// Package jev implements the shared decision API client used by CLI and MCP.
package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/koltyakov/decide/internal/fault"
)

const MaxBytes = 32 << 20

type Object = map[string]any

// Decode preserves numbers and rejects trailing JSON values.
func Decode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return fault.New(fault.Usage, "JSON input must be valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(target); err != nil {
		return fault.New(fault.Usage, "invalid JSON input")
	}
	if d.Decode(new(any)) != io.EOF {
		return fault.New(fault.Usage, "input must contain exactly one JSON value")
	}
	return nil
}

func Read(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "input exceeds 32 MiB limit")
	}
	return b, nil
}

func Question(kind string, state any, instructions string, criteria any) Object {
	q := Object{"type": kind, "instructions": instructions}
	if criteria != nil {
		q["criteria"] = criteria
	}
	return Object{"state": state, "questions": Object{"answer": q}}
}

func Validate(request Object) error {
	bad := func(message string) error { return fault.New(fault.Usage, message) }
	if !content(request["state"], false) {
		return bad("state must be text, an object, or an array")
	}
	if err := validateTree(request["state"], 0); err != nil {
		return err
	}
	if model, ok := request["model"]; ok && !nonempty(model) {
		return bad("model must be a non-empty string")
	}
	questions, ok := request["questions"].(map[string]any)
	if !ok || len(questions) == 0 {
		return bad("questions must be a non-empty object")
	}
	for name, value := range questions {
		q, ok := value.(map[string]any)
		if strings.TrimSpace(name) == "" || !ok || !content(q["instructions"], true) {
			return bad("each question requires a name; instructions must be text, object, array, or null")
		}
		for _, field := range []string{"instructions", "criteria"} {
			if err := validateTree(q[field], 0); err != nil {
				return err
			}
		}
		switch q["type"] {
		case "noul":
			if v := q["criteria"]; v != nil {
				criteria, ok := v.(map[string]any)
				if !ok {
					return bad("noul criteria must define true and/or false")
				}
				for key, value := range criteria {
					if (key != "true" && key != "false") || !content(value, true) {
						return bad("noul criteria accept true/false text, object, array, or null")
					}
				}
			}
		case "box":
			if _, exists := q["criteria"]; exists {
				return bad("box does not accept criteria")
			}
		case "choice":
			options, ok := q["criteria"].(map[string]any)
			if !ok || len(options) < 1 || len(options) > 255 {
				return bad("choice requires 1 to 255 options")
			}
			for key, description := range options {
				if strings.TrimSpace(key) == "" || !content(description, true) {
					return bad("choice keys must be non-empty; descriptions must be text, object, array, or null")
				}
			}
		case "score":
			levels, ok := q["criteria"].([]any)
			if !ok || len(levels) < 1 || len(levels) > 10 {
				return bad("score requires 1 to 10 ordered levels")
			}
			for _, level := range levels {
				if !content(level, false) {
					return bad("score levels must be text, objects, or arrays")
				}
			}
		default:
			return bad(fmt.Sprintf("question %q type must be noul, choice, score, or box", name))
		}
	}
	return nil
}

func validateTree(value any, depth int) error {
	if depth > 64 {
		return fault.New(fault.Usage, "content exceeds 64 nested levels")
	}
	switch v := value.(type) {
	case json.Number:
		n, err := v.Float64()
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return fault.New(fault.Usage, "JSON numbers must be finite; send large identifiers as strings")
		}
	case map[string]any:
		for _, item := range v {
			if err := validateTree(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateTree(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func content(v any, nullable bool) bool {
	switch v.(type) {
	case string, map[string]any, []any:
		return true
	case nil:
		return nullable
	default:
		return false
	}
}

func nonempty(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}
