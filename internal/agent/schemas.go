package agent

import (
	"slices"

	"github.com/koltyakov/decide/internal/jev"
)

func capabilitiesOutputSchema() jev.Object {
	object := func(properties jev.Object) jev.Object {
		required := make([]string, 0, len(properties))
		for key := range properties {
			required = append(required, key)
		}
		slices.Sort(required)
		return jev.Object{"type": "object", "properties": properties, "required": required}
	}
	boolean := jev.Object{"type": "boolean"}
	text := jev.Object{"type": "string"}
	return object(jev.Object{
		"provider": text, "family": text, "model": text,
		"capabilities": object(jev.Object{
			"images": jev.Object{"type": "string", "enum": []string{"none", "multipart", "base64", "parts"}},
			"box":    boolean, "longContext": boolean, "models": boolean,
			"compression": jev.Object{"type": "array", "items": text},
		}),
		"permissions":   object(jev.Object{"experimentalImages": boolean, "longContext": boolean, "compression": text}),
		"available":     object(jev.Object{"images": boolean, "box": boolean, "longContext": boolean}),
		"questionTypes": jev.Object{"type": "array", "items": jev.Object{"type": "string", "enum": []string{"noul", "choice", "score", "box"}}},
		"limits": object(jev.Object{
			"bodyBytes": jev.Object{"type": "integer", "minimum": 0}, "imageBytes": jev.Object{"type": "integer", "minimum": 0}, "contentDepth": jev.Object{"type": "integer", "minimum": 0},
		}),
		"verified": jev.Object{"const": false},
	})
}

// Schemas describe validated decisions without requiring optional provider metadata.
// Confidence is preserved verbatim; only an explicit confidence gate validates it.
func decisionOutputSchema(kind string) jev.Object {
	answerTypes := []any{}
	for _, primitive := range []string{"noul", "choice", "score", "box"} {
		if kind != "systemone" && kind != primitive {
			continue
		}
		value := jev.Object{"type": "number", "minimum": 0}
		switch primitive {
		case "noul":
			value["maximum"] = 1
		case "score":
			value["maximum"] = 9
		case "choice":
			value = jev.Object{"type": "string"}
		case "box":
			edges := jev.Object{}
			for _, edge := range []string{"top", "bottom", "left", "right"} {
				edges[edge] = jev.Object{"type": "number", "minimum": 0, "maximum": 1}
			}
			value = jev.Object{"type": "object", "required": []string{"top", "bottom", "left", "right"}, "properties": edges}
		}
		answerTypes = append(answerTypes, jev.Object{"type": "object", "required": []string{primitive}, "properties": jev.Object{
			primitive: value, "type": jev.Object{"const": primitive},
			"confidence":    jev.Object{"description": "Provider confidence, possibly absent or null. Invalid confidence fails an explicit minConfidence gate, even at zero."},
			"probabilities": jev.Object{"description": "Native probability metadata when returned"}, "legend": jev.Object{"description": "Native score rubric when returned"},
		}})
	}
	return jev.Object{"type": "object", "description": "Native decision response with optional _decide gate metadata, or an offline dryRun preview. Provider extension fields are retained.", "anyOf": []any{
		jev.Object{"required": []string{"answers"}, "properties": jev.Object{"answers": jev.Object{"type": "object", "minProperties": 1, "additionalProperties": jev.Object{"anyOf": answerTypes}}}},
		jev.Object{"required": []string{"provider", "model", "request", "headers", "bodyBytes", "uncompressedBodyBytes", "compression", "uploads"}, "properties": jev.Object{
			"provider": jev.Object{"type": "string"}, "model": jev.Object{"type": "string"}, "request": jev.Object{"type": "object"}, "headers": jev.Object{"type": "object"},
			"bodyBytes": jev.Object{"type": "integer", "minimum": 0}, "uncompressedBodyBytes": jev.Object{"type": "integer", "minimum": 0},
			"compression": jev.Object{"type": "string"}, "uploads": jev.Object{"type": "array"},
		}},
	}}
}
