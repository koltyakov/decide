# API references

Implementation checked against TypeSafe's current published documentation on October 26, 2026:

- [System One API](https://docs.typesafe.ai/api/system-one.md)
- [Images, multipart uploads, references, and limits](https://docs.typesafe.ai/images.md)
- [Box questions](https://docs.typesafe.ai/primitives/box.md)
- [Structured instructions and criteria](https://docs.typesafe.ai/primitives/advanced.md)
- [Request rules and errors](https://docs.typesafe.ai/api/errors.md)
- [Model discovery](https://docs.typesafe.ai/api/list-models.md)

The latest stable official model is Jev 1.14.0. `jev-latest` tracks stable; `jev-preview` is explicit opt-in and currently points to the same version. See [TypeSafe's models and aliases](https://docs.typesafe.ai/models.md).

Images and Box are experimental contracts. On official/custom routes they require `TypeSafe-Danger-Zone-Images: true`, set by this CLI only after process-level opt-in. TypeSafe also accepts `TypeSafe-Danger-Zone-Long-Context: true`, exposed as `--long-context`. Other providers use their own image and context contracts, not a blind forwarding of those headers.

The official compression contract is exposed as `--compression none|gzip|x-gzip|zstd` on official/custom routes. Both compressed and uncompressed bodies must fit 32 MiB, and zstd's window must not exceed 8 MiB. MCP receives this setting only through server startup options. Model catalogs are available through the CLI and MCP `models`; offline local capability inspection is client functionality, not an upstream API endpoint.

Provider contracts and current model identifiers are linked in [provider references](providers.md), including DigitalOcean, Cloudflare Jev, AI/ML API, OpenRouter, Vercel, and the separate Clef model family. Tests exercise these translations against fake responses, not live credentials. A new Jev version on one provider does not imply feature parity on every gateway. Model discovery is available for official/custom, DigitalOcean, and OpenRouter Decisions catalogs; documented version pins may be accepted without appearing in a catalog.

The CLI and MCP use native `state`, `instructions`, `criteria`, `questions`, and `assets`, with `systemone` for complete requests. Provider connections, preflight, batch processing, and optional `_decide` policy metadata are client functionality, not additional TypeSafe API fields.

For OpenCode V2 configuration, the source is [its MCP documentation](https://opencode.ai/v2/docs/mcp-servers). The global/project integration layout follows the local Control project, including client-specific Codex TOML and VS Code MCP configuration.

Current host discovery paths and their dated sources are listed in [agent integrations](agents.md#client-references). The bundled `decide` skill is independent of [TypeSafe's official SDK skill](https://docs.typesafe.ai/agent-skill.md); it teaches this CLI rather than installing an SDK or changing agent permissions.
