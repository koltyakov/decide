package agent

import (
	"context"
	"encoding/json"

	"github.com/koltyakov/decide/internal/jev"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func modelSchema() jev.Object {
	return jev.Object{"type": "string", "minLength": 1, "description": "Optional model override within the configured provider. Use models to query its native catalog; capabilities checks local support for a pin. Never switches providers."}
}

func annotations(openWorld, idempotent bool) *mcp.ToolAnnotations {
	destructive := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive, OpenWorldHint: &openWorld, IdempotentHint: idempotent}
}

func addDiscovery(s *mcp.Server, client jev.Client, defaults jev.Options) {
	addTool(s, &mcp.Tool{
		Name: "capabilities", Title: "Decide capabilities", Description: "Inspect Decide's effective provider/model, supported question types, image and Box permissions, long-context opt-in, compression, and local byte limits. Offline: no API call, key lookup, or verification of live availability. Optional model checks a pin without changing server settings.",
		InputSchema:  jev.Object{"type": "object", "properties": jev.Object{"model": modelSchema()}, "additionalProperties": false},
		OutputSchema: capabilitiesOutputSchema(), Annotations: annotations(false, true),
	}, func(_ context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Model string `json:"model"`
		}
		if len(call.Params.Arguments) > 0 {
			if err := jev.Decode(call.Params.Arguments, &args); err != nil {
				return toolError(err), nil
			}
		}
		o := defaults
		if args.Model != "" {
			o.Model = args.Model
		}
		result, err := client.Describe(o)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result), nil
	})
	addTool(s, &mcp.Tool{
		Name: "models", Title: "Decide model catalog", Description: "Explicitly query the configured provider's native model catalog with its configured credential. Supported on official/custom, DigitalOcean, and OpenRouter Decisions routes. Sends a GET, not billable inference; never changes provider or settings. Aliases and catalog access do not prove inference availability for every model/account tier.",
		InputSchema: jev.Object{"type": "object", "properties": jev.Object{}, "additionalProperties": false},
		OutputSchema: jev.Object{"type": "object", "anyOf": []any{
			jev.Object{"required": []string{"models"}, "properties": jev.Object{"models": jev.Object{"type": "array", "items": jev.Object{"type": "object", "required": []string{"name"}, "properties": jev.Object{"name": jev.Object{"type": "string"}}}}}},
			jev.Object{"required": []string{"data"}, "properties": jev.Object{"data": jev.Object{"type": "array", "items": jev.Object{"type": "object", "required": []string{"id"}, "properties": jev.Object{"id": jev.Object{"type": "string"}}}}}},
		}}, Annotations: annotations(true, true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := client.Models(ctx, defaults)
		if err != nil {
			return toolError(err), nil
		}
		return toolResult(result), nil
	})
}

func toolResult(value any) *mcp.CallToolResult {
	b, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: value}
}
