package agent

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The SDK's low-level AddTool publishes schemas but leaves validation to us.
func addTool(server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandler) {
	data, err := json.Marshal(tool.InputSchema)
	if err != nil {
		panic(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	server.AddTool(tool, func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input any
		arguments := call.Params.Arguments
		if len(arguments) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		// jsonschema-go expects float64 numeric types for type validation.
		// Handlers decode the original bytes separately to preserve exact numbers.
		if !utf8.Valid(arguments) || json.Unmarshal(arguments, &input) != nil {
			return toolError(fault.New(fault.Usage, "invalid JSON arguments")), nil
		}
		if err := resolved.Validate(input); err != nil {
			// Schema errors can include private values. Return only the contract.
			return toolError(fault.New(fault.Usage, "invalid "+tool.Name+" arguments; follow the advertised input schema")), nil
		}
		return handler(ctx, call)
	})
}
