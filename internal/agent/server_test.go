package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
	"github.com/koltyakov/decide/skills"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPToolsWithRealProtocol(t *testing.T) {
	client := testutil.Client(t)
	var calls atomic.Int32
	api := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		request, _ := testutil.Request(t, r)
		if state, ok := request["state"].(map[string]any); ok && state["id"] != json.Number("9007199254740993") {
			t.Error("MCP schema validation changed an exact JSON number")
		}
		answers := jev.Object{}
		for name, v := range request["questions"].(map[string]any) {
			kind := v.(map[string]any)["type"].(string)
			var value any = 0.8
			switch kind {
			case "choice":
				value = "a"
			case "box":
				value = jev.Object{"left": 0, "top": 0, "right": 1, "bottom": 1}
			}
			answers[name] = jev.Object{kind: value}
		}
		testutil.Reply(t, w, jev.Object{"answers": answers})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := Server(client, jev.Options{Endpoint: api.URL, Images: true}, "test")
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	image := testutil.PNG(t, 280, 280)
	for _, tt := range []struct {
		name string
		args jev.Object
	}{
		{"noul", jev.Object{"state": "@not-a-file", "instructions": jev.Object{"task": "Q?"}, "criteria": jev.Object{"true": "yes", "false": "no"}}},
		{"noul", jev.Object{"state": jev.Object{"id": json.Number("9007199254740993")}}},
		{"choice", jev.Object{"state": "state", "instructions": "Q?", "criteria": jev.Object{"a": "Alpha", "b": "Beta"}}},
		{"score", jev.Object{"state": "state", "instructions": "Q?", "criteria": []any{"Low", "High"}}},
		{"systemone", jev.Object{"request": jev.Question("noul", "state", "Q?", nil)}},
		{"box", jev.Object{"state": "photo-ref", "instructions": "Find mug", "assets": jev.Object{"photo-ref": jev.Object{"format": "png", "data_base64": base64.StdEncoding.EncodeToString(image)}}}},
	} {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
		if err != nil || result.IsError || result.StructuredContent == nil {
			t.Fatalf("%s: %v %v", tt.name, result, err)
		}
		for _, tool := range tools.Tools {
			if tool.Name == tt.name {
				validateOutput(t, tool, result)
			}
		}
	}
	before := calls.Load()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "choice", Arguments: jev.Object{"state": "x", "instructions": "Q?", "criteria": jev.Object{}}})
	if err == nil && !result.IsError {
		t.Error("invalid request accepted")
	}
	if calls.Load() != before {
		t.Error("invalid request reached provider")
	}
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "noul", Arguments: jev.Object{"state": "x", "instructions": "Q?", "policy": jev.Object{"atLeast": 0.9}}})
	if err != nil || result.IsError {
		t.Fatalf("gate rejection was treated as a tool error: %v %v", result, err)
	}
	var response jev.Object
	if err = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &response); err != nil {
		t.Fatal(err)
	}
	if response["_decide"].(map[string]any)["gate"].(map[string]any)["passed"] != false || response["answers"] == nil {
		t.Fatal("native response or gate metadata missing")
	}
	before = calls.Load()
	result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "noul", Arguments: jev.Object{"state": "x", "instructions": "Q?", "dryRun": true}})
	if err != nil || result.IsError || calls.Load() != before {
		t.Fatalf("MCP preflight reached API: %v %v", result, err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "noul" {
			validateOutput(t, tool, result)
		}
	}
}

func protocolSession(t *testing.T, client jev.Client, defaults jev.Options) (context.Context, *mcp.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := Server(client, defaults, "test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return ctx, cs
}

func validateOutput(t *testing.T, tool *mcp.Tool, result *mcp.CallToolResult) {
	t.Helper()
	schemaJSON, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatalf("%s output violates advertised schema: %v", tool.Name, err)
	}
}

func TestDiscoveryIsOfflineWithoutCredentials(t *testing.T) {
	client := testutil.Client(t)
	for _, provider := range jev.Providers() {
		t.Setenv(provider.KeyEnv, "")
	}
	var calls atomic.Int32
	api := testutil.API(t, func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) })
	if err := client.Credentials.SaveConnection("official", "", credentials.Connection{Endpoint: api.URL + "/v1/systemone", Model: "jev-1.13.0"}); err != nil {
		t.Fatal(err)
	}
	ctx, cs := protocolSession(t, client, jev.Options{Images: true, Compression: "gzip"})
	if !strings.Contains(cs.InitializeResult().Instructions, "capabilities") {
		t.Fatal("initialization lacks discovery instructions")
	}
	noArgs, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "capabilities"})
	if err != nil || noArgs.IsError {
		t.Fatalf("no-argument discovery: %v %v", noArgs, err)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Description == "" || tool.Title == "" || tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("incomplete discovery metadata", tool)
		}
		if tool.Name != "models" && tool.Name != "capabilities" && tool.Annotations.IdempotentHint {
			t.Fatal("billable decisions advertised as idempotent")
		}
		if tool.Name == "capabilities" && (tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint) {
			t.Fatal("offline discovery advertised as external")
		}
	}
	for _, args := range []jev.Object{{}, {"model": "jev-latest"}} {
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "capabilities", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("capabilities: %v %v", result, err)
		}
		var value jev.Object
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &value); err != nil {
			t.Fatal(err)
		}
		wantBox := args["model"] == "jev-latest"
		if value["available"].(map[string]any)["box"] != wantBox || value["verified"] != false || value["permissions"].(map[string]any)["compression"] != "gzip" {
			t.Fatal(value)
		}
		for _, tool := range tools.Tools {
			if tool.Name == "capabilities" {
				validateOutput(t, tool, result)
			}
		}
	}
	resources, err := cs.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != "decide://skills/decide" {
		t.Fatalf("resources: %v %v", resources, err)
	}
	skill, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "decide://skills/decide"})
	if err != nil || len(skill.Contents) != 1 || skill.Contents[0].Text != string(skills.Decide) {
		t.Fatalf("embedded skill: %v %v", skill, err)
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "models", Arguments: jev.Object{}})
	if err != nil || !result.IsError {
		t.Fatal("catalog unexpectedly succeeded without a key", result, err)
	}
	if calls.Load() != 0 {
		t.Fatal("discovery sent a provider request")
	}
}

func TestModelCatalogAndTrustBoundaries(t *testing.T) {
	for _, provider := range []string{"official", "digitalocean", "openrouter", "aimlapi"} {
		t.Run(provider, func(t *testing.T) {
			client := testutil.Client(t)
			var calls atomic.Int32
			api := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Content-Encoding") != "" {
					t.Error("catalog must be an uncompressed GET")
				}
				if provider == "official" {
					testutil.Reply(t, w, jev.Object{"models": []any{jev.Object{"name": "jev-latest", "description": "Catalog entry", "release_date": "2026-10-14"}}})
				} else {
					if provider == "openrouter" && r.URL.Query().Get("output_modalities") != "decisions" {
						t.Error("wrong catalog filter")
					}
					testutil.Reply(t, w, jev.Object{"data": []any{jev.Object{"id": "native-id"}}, "native": true})
				}
			})
			endpoint := api.URL + "/v1/systemone"
			if provider == "openrouter" {
				endpoint = api.URL + "/api/alpha/decisions"
			}
			ctx, cs := protocolSession(t, client, jev.Options{Provider: provider, Endpoint: endpoint})
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "models", Arguments: jev.Object{}})
			if provider == "aimlapi" {
				if err != nil || !result.IsError || calls.Load() != 0 {
					t.Fatal("unsupported catalog reached network", result, err)
				}
			} else {
				if err != nil || result.IsError || calls.Load() != 1 {
					t.Fatalf("catalog: %v %v", result, err)
				}
				tools, err := cs.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, tool := range tools.Tools {
					if tool.Name == "models" {
						validateOutput(t, tool, result)
					}
				}
			}
			before := calls.Load()
			for _, name := range []string{"models", "capabilities", "noul"} {
				for _, field := range []string{"provider", "endpoint", "experimentalImages", "longContext", "compression"} {
					args := jev.Object{field: "untrusted"}
					if name == "noul" {
						args["state"] = "x"
					}
					result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
					if err == nil && !result.IsError {
						t.Fatal("tool accepted process configuration", name, field)
					}
				}
			}
			if calls.Load() != before {
				t.Fatal("invalid tool arguments reached provider")
			}
			for _, args := range []jev.Object{
				{"state": "private-evidence", "dryRun": "true"},
				{"state": "private-evidence", "model": nil},
				{"state": "private-evidence", "policy": jev.Object{"atLeast": "0.9"}},
			} {
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "noul", Arguments: args})
				if err != nil || !result.IsError {
					t.Fatalf("invalid types accepted: %v %v", result, err)
				}
				if strings.Contains(result.Content[0].(*mcp.TextContent).Text, "private-evidence") {
					t.Fatal("schema error leaked supplied evidence")
				}
			}
		})
	}
}
