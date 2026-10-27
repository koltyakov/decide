# Agent integrations

Install MCP with `decide install-mcp CLIENT`, the bundled skill with `decide install-skill CLIENT`, or both. Global is the default. `--project DIR` installs into an existing project instead. These commands follow Control's client layouts.

The bundled skill teaches CLI scenarios, native payloads, answer interpretation, and workflow controls. MCP setup and tool contracts belong here; connection setup, model versions, and capability differences are documented in [providers](providers.md).

| Client | Global MCP | Project MCP | Global skill directory | Project skill directory |
| --- | --- | --- | --- | --- |
| OpenCode | `$XDG_CONFIG_HOME/opencode/opencode.json` | `opencode.json` | `$XDG_CONFIG_HOME/opencode/skills/decide` | `.opencode/skills/decide` |
| Claude Code | `~/.claude.json` | `.mcp.json` | `~/.claude/skills/decide` | `.claude/skills/decide` |
| Codex | `~/.codex/config.toml` | `.codex/config.toml` | `~/.agents/skills/decide` | `.agents/skills/decide` |
| Cursor | `~/.cursor/mcp.json` | `.cursor/mcp.json` | `~/.cursor/skills/decide` | `.cursor/skills/decide` |
| Copilot | `~/.copilot/mcp-config.json` | `.vscode/mcp.json` | `~/.copilot/skills/decide` | `.github/skills/decide` |
| Devin | `$XDG_CONFIG_HOME/devin/mcp_config.json` | `.devin/mcp_config.json` | `$XDG_CONFIG_HOME/devin/skills/decide` | `.devin/skills/decide` |
| Legacy Windsurf | `~/.codeium/windsurf/mcp_config.json` | Not supported | `~/.codeium/windsurf/skills/decide` | `.windsurf/skills/decide` |
| Antigravity App/IDE | `~/.gemini/config/mcp_config.json` | `.agents/mcp_config.json` | `~/.gemini/config/skills/decide` | `.agents/skills/decide` |
| Generic agents | `~/.agents/mcp.json` | `.agents/mcp.json` | `~/.agents/skills/decide` | `.agents/skills/decide` |

`XDG_CONFIG_HOME` defaults to `~/.config`. Each skill directory contains `SKILL.md`. For direct JSON config editing, an existing `.jsonc` is preferred over `.json`. OpenCode uses its native CLI when available so it can choose the effective config file. Generic agents configuration may require manual import.

On Windows, Devin uses `%APPDATA%\devin` for global configuration and skills. Use client `devin` for current Devin Desktop/CLI. Client `windsurf` preserves the older Windsurf global layout; `install-mcp windsurf --project DIR` fails before writing anything because that legacy project MCP path is not documented. Project skill installation still works. Current Cascade and Devin Local use different agents, but their current global MCP file is the Devin path above.

Codex project MCP configuration requires a trusted project. Antigravity CLI reads the same global/workspace MCP files, but its global skills use `~/.gemini/antigravity-cli/skills`; use a project skill or copy the embedded skill there when targeting that CLI specifically. Existing Codex or Antigravity skills/configs at legacy paths are not deleted or migrated automatically. Review old copies before removing duplicates yourself.

## Automatic discovery

Installation is opt-in. Once installed, supported hosts scan their skill directories and advertise the skill's `name` and `description`. The model loads the body when the task matches, or the user invokes the skill by name. Discovery is not a promise that every prompt will trigger it; host permissions, skill budgets, and relevance matching still apply.

MCP initialization returns server instructions that explain tool selection, offline discovery, billing, and safety boundaries. `tools/list` advertises all seven tools with titles, descriptions, input/output schemas, and annotations. Initialization, listing tools/resources, and reading the embedded skill need no key and do not contact a provider. `capabilities` is also offline. Only an explicit `models` or non-dry-run decision call contacts the configured provider.

The resource `decide://skills/decide` returns the exact embedded `SKILL.md` as `text/markdown`. This lets MCP-only hosts retrieve CLI usage guidance without installing a skill. It does not install anything or replace MCP argument schemas. Hosts that do not support resources can still use every tool.

## Manual MCP configuration

Most hosts accept:

```json
{
  "mcpServers": {
    "decide": {
      "type": "stdio",
      "command": "/absolute/path/to/decide",
      "args": ["mcp"]
    }
  }
}
```

OpenCode V2:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "decide": {
        "type": "local",
        "command": ["/absolute/path/to/decide", "mcp"]
      }
    }
  }
}
```

For image support, add `--experimental-images` to the process arguments, not the tool call. `--long-context` and `--compression gzip|x-gzip|zstd` are also process options for supporting providers. `install-mcp` retains these options in the generated command. Credentials come from the saved store or inherited provider environment. Never put a key in the command array. The generated integration pins the credential file's absolute path, so the server's working directory does not change credential lookup.

Provider defaults follow current documented Jev versions: official `jev-latest`, OpenRouter `~typesafe/jev-latest`, AI/ML API `typesafe/jev`, and Vercel `typesafe-ai/jev-1.14.0`. Saved/request model pins remain authoritative. DigitalOcean and Cloudflare Jev are not assumed to expose the newest features; Clef is a separate model family. See [provider contracts and versions](providers.md).

## MCP tool contract

| Tool | Required arguments | Optional arguments |
| --- | --- | --- |
| `noul` | `state` | `instructions`, `criteria`, `assets`, `model`, `policy`, `dryRun` |
| `choice` | `state`, `criteria` object | `instructions`, `assets`, `model`, `policy`, `dryRun` |
| `score` | `state`, `criteria` array | `instructions`, `assets`, `model`, `policy`, `dryRun` |
| `box` | `state` | `instructions`, `assets`, `model`, `policy`, `dryRun` |
| `systemone` | `request` | `model`, `policy`, `dryRun` |
| `capabilities` | None | `model` |
| `models` | None | None |

The names mirror the API, including `instructions` and `criteria`. Instructions can be text or structured JSON. `state` is literal text, an object, or an array. No string in a tool argument triggers filesystem access. The server validates every call against its advertised input schema before running its handler. Unknown top-level arguments and invalid types fail locally; unknown native request/question extension fields remain allowed inside `systemone.request`. Schema errors omit supplied values. A tool error sets MCP's `isError`; successful results include both text JSON and `structuredContent`.

Decision output schemas describe native `answers` or a `dryRun` preview and retain extension metadata. They do not invent required metadata that a provider may omit. Confidence remains provider-owned; its presence and validity are checked only when the caller requests a confidence gate. All tools are read-only. Decision tools are not marked idempotent because repeated calls may incur new charges. `models` is an external read; `capabilities` is a local read. Annotations are hints, not authorization or a reason to retry.

`capabilities` reports `provider`, `family`, `model`, published `capabilities`, supported `questionTypes`, process `permissions`, enabled `available` image/Box/long-context features, and local `limits`. `verified` is always false. A model argument inspects a pin within the configured provider without saving it. It does not load a key, expose credential paths or endpoints, probe an account, or predict server context fit. Use it before constructing requests for provider-specific features.

`models` makes an explicit authenticated GET on official/custom, DigitalOcean, or OpenRouter Decisions routes and retains the native catalog shape. Unsupported providers fail locally. Discovery does not change provider, endpoint, or saved settings, and catalog access does not prove inference availability for every listed model/account tier.

Optional `policy` accepts `answer`, `atLeast`, `is`, and `minConfidence`. All supplied rules must pass; a multi-question request requires an explicit `answer`. Gate rejection is a valid response with `_decide.gate.passed: false`, not a tool error. Missing confidence fails closed. `dryRun: true` returns a local preflight preview with no API call or key requirement. See [workflow rules](workflows.md).

Inline asset example:

```json
{
  "state": {"photo": "unique-image-ref"},
  "instructions": "Is there a person in photo?",
  "assets": {
    "unique-image-ref": {"format": "jpg", "data_base64": "<standard padded base64>"}
  }
}
```

The same asset input works on official/custom, AI/ML API, and OpenRouter. Official/custom forward native assets; AI/ML API uses inline assets; OpenRouter converts references to inline `image_url` parts, including inside instructions and criteria. Unsupported providers reject the request before an API call. Native OpenRouter data-URL image parts can also be supplied in literal state or question content; they cannot bypass opt-in or limits, and remote URLs are never fetched. AI/ML API requires instructions on each question and at least two Score levels, even though the shared native schemas allow broader shapes.

Tool discovery and initialization do not require a key or send an API request. Calls validate input, policy, and experimental permissions before contacting the provider. The process and saved provider connection control endpoint selection, compression, and experimental permissions. Models may be overridden by a decision or capabilities argument. Configure a connection outside chat using `decide providers connect PROVIDER`; never ask an agent to read a credential value.

## Client references

Paths and discovery contracts checked on October 26, 2026:

- [OpenCode V2 skills](https://opencode.ai/v2/docs/skills) and [MCP](https://opencode.ai/v2/docs/mcp-servers)
- [Claude Code skills](https://code.claude.com/docs/en/skills) and [MCP](https://code.claude.com/docs/en/mcp)
- [Codex skills](https://developers.openai.com/codex/skills/) and [MCP](https://developers.openai.com/codex/mcp/)
- [Cursor skills](https://cursor.com/docs/skills)
- [Copilot skills](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills)
- [Devin MCP configuration](https://docs.devin.ai/cli/extensibility/mcp/configuration) and [skills](https://docs.devin.ai/cli/extensibility/skills/overview)
- [Cascade MCP](https://docs.devin.ai/desktop/cascade/mcp) and [skills](https://docs.devin.ai/desktop/cascade/skills)
- [Antigravity MCP](https://antigravity.google/docs/mcp/) and [skills](https://antigravity.google/docs/skills/)
