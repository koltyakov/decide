// Package agent exposes the native Jev request vocabulary over MCP.
package agent

import (
	"context"
	"encoding/json"

	"github.com/koltyakov/decide/internal/decision"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/skills"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Instructions = "Decide provides access to Jev, TypeSafe AI's typed decision model. Use capabilities first to inspect the configured provider/model and process permissions offline. models explicitly queries its catalog, not inference. Decision calls send authorized state to the provider and may be billed; never retry uncertain calls automatically. Model answers never authorize downstream actions. Use noul for yes/no probability, choice for classification, score for a rubric, box for image localization, and systemone for independent named questions. Keep native state, instructions, criteria, questions, and assets. Results preserve answers, probabilities, confidence, legend, model, and usage. Optional policy adds client-owned _decide.gate metadata; rejected gates are valid results, not tool errors. dryRun validates and previews supplied private content without an API call or a key. Images and box require process-level experimental opt-in and a supporting provider/model. Inline PNG/JPEG assets never read local paths or fetch URLs. Box has no not-found result; pair it with a presence noul. Provider, endpoint, compression, and experimental permissions are configured outside tool arguments. The embedded CLI usage skill is available as the resource decide://skills/decide; it is separate from these MCP tool schemas."

func Server(client jev.Client, defaults jev.Options, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "decide", Version: version}, &mcp.ServerOptions{Instructions: Instructions})
	s.AddResource(&mcp.Resource{URI: "decide://skills/decide", Name: "decide", Title: "Decide CLI usage skill", MIMEType: "text/markdown", Size: int64(len(skills.Decide)), Description: "Embedded Decide CLI skill for TypeSafe AI decisions, native payloads, image localization, batches, and explicit gates. CLI guidance, not MCP argument syntax."}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "decide://skills/decide", MIMEType: "text/markdown", Text: string(skills.Decide)}}}, nil
	})
	addDiscovery(s, client, defaults)
	text := func(description string) jev.Object { return jev.Object{"type": "string", "description": description} }
	entry := func(description string) jev.Object {
		return jev.Object{"description": description, "anyOf": []any{jev.Object{"type": "string"}, jev.Object{"type": "object"}, jev.Object{"type": "array"}, jev.Object{"type": "null"}}}
	}
	state := jev.Object{"description": "Native state: text, object, or array, sent verbatim. Strings never read files.", "anyOf": []any{jev.Object{"type": "string"}, jev.Object{"type": "object"}, jev.Object{"type": "array"}}}
	assets := jev.Object{"type": "object", "minProperties": 1, "description": "Images keyed by opaque references used in state, instructions, or criteria. Requires experimental images enabled at server startup. Never local paths.", "additionalProperties": jev.Object{
		"type": "object", "required": []string{"format", "data_base64"}, "properties": jev.Object{
			"type": jev.Object{"type": "string", "const": "image"}, "format": jev.Object{"type": "string", "enum": []string{"png", "jpg", "jpeg"}},
			"data_base64": text("Standard padded base64 PNG/JPEG bytes, no data URL or whitespace. Each dimension 280..1568 for TypeSafe/custom or 256..1568 for AIMLAPI/OpenRouter; OpenRouter also requires 78,400 pixels total. At most 16 MiB decoded. Provider image support is configured outside tools."),
		},
	}}
	criteria := map[string]jev.Object{
		"noul":   {"type": "object", "properties": jev.Object{"true": entry("What counts as yes"), "false": entry("What counts as no")}, "additionalProperties": false},
		"choice": {"type": "object", "minProperties": 1, "maxProperties": 255, "additionalProperties": entry("Criterion description, including structured examples or image references")},
		"score":  {"type": "array", "minItems": 1, "maxItems": 10, "items": jev.Object{"description": "Ordered level description, zero-based. Score may be fractional.", "anyOf": state["anyOf"]}},
	}
	criteria["noul"] = jev.Object{"anyOf": []any{criteria["noul"], jev.Object{"type": "null"}}}
	questionTypes := []any{}
	for _, kind := range []string{"noul", "choice", "score", "box"} {
		properties := jev.Object{"type": jev.Object{"const": kind}, "instructions": entry("Native instructions: text, object, array, or null")}
		required := []string{"type"}
		q := jev.Object{"type": "object", "properties": properties}
		if kind == "box" {
			q["not"] = jev.Object{"required": []string{"criteria"}}
		} else {
			properties["criteria"] = criteria[kind]
			if kind != "noul" {
				required = append(required, "criteria")
			}
		}
		q["required"] = required
		questionTypes = append(questionTypes, q)
	}
	question := jev.Object{"oneOf": questionTypes}
	policy := jev.Object{"type": "object", "additionalProperties": false, "description": "Optional local acceptance rules. No implicit thresholds; all rules must pass. Missing confidence fails closed.", "properties": jev.Object{
		"answer":  text("Named question to gate. Required when there are multiple questions."),
		"atLeast": jev.Object{"type": "number", "minimum": 0, "description": "Minimum noul probability or score"},
		"is":      text("Required declared choice key"), "minConfidence": jev.Object{"type": "number", "minimum": 0, "maximum": 1, "description": "Minimum confidence for choice, score, or box. Noul uses atLeast instead."},
	}}
	for _, kind := range []string{"noul", "choice", "score", "box", "systemone"} {
		properties := jev.Object{"model": modelSchema(), "policy": policy, "dryRun": jev.Object{"type": "boolean", "description": "Validate and preview private supplied content without credentials or a network request"}}
		required := []string{"state"}
		if kind == "systemone" {
			properties["request"] = jev.Object{"type": "object", "required": []string{"state", "questions"}, "properties": jev.Object{
				"state": state, "questions": jev.Object{"type": "object", "minProperties": 1, "additionalProperties": question}, "model": text("Model, unless overridden by the tool argument"), "assets": assets,
			}, "description": "Complete native System One request. Unknown members are forwarded unchanged."}
			required = []string{"request"}
		} else {
			properties["state"] = state
			properties["instructions"] = entry("Native instructions, with optional structured JSON or image references")
			properties["assets"] = assets
			if shape, ok := criteria[kind]; ok {
				properties["criteria"] = shape
			}
			if kind == "choice" || kind == "score" {
				required = append(required, "criteria")
			}
		}
		description := map[string]string{
			"noul":      "Ask Jev by TypeSafe AI a yes/no question for urgency, presence, verification, or independent tagging. Returns answers.answer.noul, a probability from 0 to 1, not a boolean or confidence. Optional true/false criteria clarify the boundary.",
			"choice":    "Classify or route supplied evidence with Jev by TypeSafe AI. Selects exactly one of 1..255 criteria keys and retains probabilities and confidence. Use several nouls for independent labels, not one choice.",
			"score":     "Rate severity, relevance, or quality with Jev by TypeSafe AI against 1..10 ordered criteria levels. Returns a probability-weighted zero-based score that can be fractional, plus any probabilities, confidence, and legend.",
			"box":       "Locate one target in an image with Jev by TypeSafe AI. Experimental; requires supporting provider/model, server image opt-in, and inline image bytes. Returns normalized top/bottom/left/right edges. No not-found result: pair with a presence noul.",
			"systemone": "Ask Jev by TypeSafe AI several independent named noul, choice, score, or box questions against shared evidence in one request. Retains native fields and extensions. Questions cannot consume each other's answers. Multi-question gates require policy.answer.",
		}[kind] + " Sends authorized evidence to the configured provider and may be billed unless dryRun is true. No automatic retries."
		addTool(s, &mcp.Tool{Name: kind, Title: "Decide " + kind, Description: description, InputSchema: jev.Object{"type": "object", "properties": properties, "required": required, "additionalProperties": false}, OutputSchema: decisionOutputSchema(kind), Annotations: annotations(true, false)}, func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args jev.Object
			if err := jev.Decode(call.Params.Arguments, &args); err != nil {
				return toolError(err), nil
			}
			o := defaults
			if value, ok := args["model"].(string); ok {
				o.Model = value
			}
			var p decision.Policy
			if value, ok := args["policy"]; ok {
				b, _ := json.Marshal(value)
				if err := jev.Decode(b, &p); err != nil {
					return toolError(err), nil
				}
			}
			var request jev.Object
			if kind == "systemone" {
				request, _ = args["request"].(map[string]any)
			} else {
				q := jev.Object{"type": kind}
				for _, key := range []string{"instructions", "criteria"} {
					if value, ok := args[key]; ok {
						q[key] = value
					}
				}
				request = jev.Object{"state": args["state"], "questions": jev.Object{"answer": q}}
				if value, ok := args["assets"]; ok {
					request["assets"] = value
				}
			}
			dryRun, _ := args["dryRun"].(bool)
			result, err := decision.Evaluate(ctx, client, request, o, p, dryRun)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(result.Response), nil
		})
	}
	return s
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}
