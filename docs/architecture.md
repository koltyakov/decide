# Architecture

`cmd/decide` is the production executable. `cmd/decide-bundle` is development tooling and is not installed by `make install`.

| Package | Responsibility |
| --- | --- |
| `internal/cli` | Native-field flags, explicit file/stdin inputs, System One recipes, bounded JSONL batches, and provider commands |
| `internal/agent` | Validated MCP input/output schemas, offline capability discovery, model catalogs, embedded skill resource, and shared workflows |
| `internal/decision` | Answer selection, acceptance policy validation, dry runs, and gate metadata |
| `internal/jev` | Provider resolution, native request/image validation, encoding, HTTP, and response validation |
| `internal/credentials` | Provider-isolated keys, connection settings, and saved default selection |
| `internal/installation` | Client paths, JSONC/TOML merges, managed skill installation |
| `internal/store` | Advisory locks and atomic private writes |
| `internal/fault` | Shared CLI error and decision status categories |
| `skills` | Embedded CLI and agent guidance |

## Request flow

CLI and MCP call `decision.Evaluate`. It resolves connection defaults, calls `jev.Prepare`, and validates the policy. Preparation copies the request, validates native questions, binds assets, translates provider-specific contracts, and encodes the bounded body. It does not require a key or contact the API.

A dry run returns a preview from this prepared request. Execution loads the selected key and calls `Client.Execute` once. Response validation rejects missing decisions, invalid primitive values, unknown Choice keys, and malformed boxes. The workflow layer adds `_decide` metadata only when a policy was explicitly requested; it does not rename API answers.

Optional gzip/x-gzip/zstd compression runs after JSON or multipart encoding on official/custom routes only. Both original and transmitted bodies are bounded to 32 MiB, and zstd's window is at most 8 MiB. Preflight reports both sizes and uses the same prepared body as execution. Compression never triggers a retry or fallback.

MCP initialization and tool/resource listing use static embedded metadata. Input schemas are enforced before handlers; the SDK's low-level registration alone does not validate calls. Schema failures omit private input values. Handlers decode original argument bytes with preserved JSON numbers. The seven tools share local inspection, model catalogs, and decision preparation with the CLI. `decide://skills/decide` serves embedded bytes without filesystem or API access. Capability inspection resolves local settings but never retrieves a key or probes a provider.

Single primitives use the question key `answer`. Multi-question calls require explicit question selection for gates or value output. Unknown native request members are preserved. JSON numbers retain their representation instead of passing through floating-point conversion before transmission.

## Provider connections

The private store holds per-provider keys, connection settings, and a default provider in one atomic document. `connect` tests an entered key in memory and commits only after success. Official and DigitalOcean verification use authenticated models GETs; other providers use a small decision POST. OpenRouter's public catalog is not used as proof of key validity. A failed verification doesn't mutate saved settings. Environment credentials aren't copied into the file.

Selection is explicit flag, process environment, saved default, then official. Endpoint and model overrides preserve request model precedence. CLI and MCP read the same connection settings. The store lives under `decide`, not another CLI's config directory, and there is no implicit credential migration.

The provider registry owns default model IDs, model family, and capabilities. Jev and Clef are separate entries. Current stable aliases are preferred where documented; Vercel uses its explicit current version, and lagging DigitalOcean/Cloudflare routes are not relabeled as latest. Explicit version pins are preserved. Capabilities reject unsupported media, Box, and opt-in headers before HTTP. Cloudflare's wrapper and envelope handling are isolated from native Jev payloads.

## Images and batches

Only explicit CLI file flags read paths. Raw JSON and MCP arguments never trigger local file access. Primitive image calls generate opaque references and named state; System One file attachments use references already in the request.

File attachments use multipart with JSON first on official/custom, inline base64 on AI/ML API, and inline `image_url` parts on OpenRouter. OpenRouter's adapter replaces references in state/instructions/criteria because that API otherwise ignores the assets map. Direct native OpenRouter parts are validated too. Reference expansion has a bounded safety budget before encoding; actual body sizes are checked afterward. Images are validated for each provider's dimensions, animation, bytes, references, and opt-in requirements. Bodies are buffered rather than uploaded by unowned pipe goroutines. Pixels are not resized or re-encoded. Server-side image processing and token limits can still reject a locally valid request.

Batch reads JSONL states into windows of at most `jobs` records, with `jobs` bounded to 1..16. Independent requests run concurrently within a window. All results are emitted in input order before the next window starts. This bounds pending work and reorder storage without buffering the input stream. Errors are record results, not invented decisions. Cancellation reaches HTTP and prevents later windows from starting.

## Trust and release boundaries

- Keys remain outside agent configuration and tool arguments.
- MCP cannot change provider, endpoint, or experimental permissions.
- HTTPS is required except on loopback; redirects and automatic retries are disabled.
- Provider error bodies are not reflected because they can contain private input or credentials.
- Probability and confidence are distinct. A gate is not permission to perform a downstream action.
- Build and tests don't install agent configuration, services, or credentials.

Build/release conventions follow Control: six CGO-free platform binaries, manifests, checksums, race tests, and pinned checks. Release CI signs metadata with Sigstore. Install scripts verify checksums and install `decide` or `decide.exe`.
