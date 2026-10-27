# Working on Decide

Decide is a standalone Go client for Jev's native decision API. The repository is `github.com/koltyakov/decide` and the installed command is `decide`. CLI commands and the stdio MCP server share request validation, provider adapters, and workflow controls. This is an independent community project, not a TypeSafe AI product.

These instructions apply throughout the repository.

## Read first

- [README](README.md): commands, input/output contracts, limits, and development entry points.
- [Architecture](docs/architecture.md): package boundaries, request flow, and trust boundaries.
- [Design decisions](docs/design.md): vocabulary, explicit file reads, connection lifecycle, and workflow rules.
- [Workflow controls](docs/workflows.md): preflight, acceptance policies, reusable requests, and JSONL batches.
- [Provider contracts](docs/providers.md): authentication, model defaults, capability differences, and upstream sources.
- [Agent integrations](docs/agents.md): MCP schemas, client configuration, and managed installation.
- [API references](docs/api-reference.md): upstream contracts used by the implementation.

Read the relevant implementation before changing a documented contract. Distinguish implemented behavior from proposed work and upstream documentation from behavior verified against a live account.

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/decide` | Production executable entry point |
| `cmd/decide-bundle` | Build tooling, release manifests, and checksum verification |
| `internal/cli` | Commands, explicit file/stdin inputs, output formatting, and bounded batches |
| `internal/agent` | MCP schemas and transport, using the shared workflow implementation |
| `internal/decision` | Answer selection, acceptance policies, preflight, and gate metadata |
| `internal/jev` | Native request/image validation, provider adapters, encoding, HTTP, and response validation |
| `internal/credentials` | Provider-isolated keys, connection settings, and default selection |
| `internal/installation` | Client configuration merges and managed MCP/skill installation |
| `internal/store` | Locking and atomic private writes |
| `internal/fault` | Shared failure categories and exit codes |
| `internal/testutil` | Local fake APIs and test fixtures |
| `skills/decide/SKILL.md`, `skills/embed.go` | CLI usage skill and its embedding in the executable |
| `examples`, `scripts`, `.github/workflows` | Native payload examples, installers, and CI/release automation |

## Implementation principles

- Keep the core in Go and preserve the minimum version in `go.mod`. Support Linux, macOS, and Windows with CGO-free builds. Keep platform-specific behavior isolated; don't assume a Unix shell on Windows.
- Keep `decide` self-contained. Control is a reference for Go infrastructure and agent integration patterns, not a runtime dependency. The Python reference under `tmp/jev-cli` is not the implementation or a dependency.
- Preserve Jev's vocabulary: `noul`, `choice`, `score`, `box`, `systemone`, `state`, `instructions`, `criteria`, `questions`, and `assets`. Don't introduce a renamed question language or universal answer schema.
- Share behavior through `internal/jev` and `internal/decision`. Keep CLI and MCP thin and consistent. Put provider-specific translation in `internal/jev`, not in CLI commands or MCP handlers.
- Preserve native request extension fields and response metadata. Copy caller-owned requests before preparation, retain JSON number representations, and validate returned answers before treating them as decisions.
- Keep literal values separate from file access. Only explicit CLI file flags and request-file arguments read paths. `@path`, JSON values, MCP arguments, and batch rows never trigger implicit file reads. Allow only one stdin consumer per invocation.
- Use the same validation and encoder for preflight and execution. Dry runs require no key or network access. Their output contains supplied content, so don't treat previews as sanitized logs.
- Propagate contexts and cancellation. Bound request bodies, image-reference expansion, batch concurrency, and reorder buffers. Close owned files and HTTP bodies, and avoid unowned goroutines.
- Keep batch records independent and results in input order. Report record failures explicitly; don't turn failures into decisions or buffer an entire input stream.
- Preserve explicit image opt-in, format/dimension/byte validation, and reference rules. Don't silently resize, crop, rotate, fetch remote image URLs, or act on Box coordinates. A Box has no not-found result; pair localization with a presence question.

## Decisions and failure behavior

- Gates use caller-supplied thresholds only. Noul probability and answer confidence are different quantities. Missing or invalid confidence fails a confidence gate, including at threshold zero.
- Require explicit answer selection for gates or value output when multiple questions exist. Questions in one request are independent, not a chain that consumes prior answers.
- Preserve native answers when adding client-owned `_decide` policy/gate metadata. A valid gate rejection is exit 5, not an API failure or MCP tool error. Batch record failures use exit 6; keep the remaining categories in `internal/fault` stable.
- Keep responses on stdout and JSON errors on stderr. Don't mix progress output into JSON or MCP stdio.
- Never automatically retry an uncertain or potentially billable request, downgrade a model, or switch providers after failure. A model answer is not authorization for a downstream action.

## Credentials and provider boundaries

- Keep keys out of command arguments, MCP arguments/configuration, logs, and error messages. Use dummy keys in test fixtures, never real credentials. Don't reflect provider error bodies, which may echo private input or credentials.
- Preserve provider-isolated credentials and documented selection precedence. Verify connections before atomically saving settings or entered keys. Failed verification must not mutate saved state; environment keys must not be copied into the store implicitly.
- Require HTTPS except on loopback and keep redirects disabled. Endpoint overrides receive credentials, so they must remain explicit trusted configuration. MCP tool calls cannot change provider, endpoint, or experimental permissions.
- Keep capability checks provider- and model-specific, before HTTP. Check current upstream sources before changing defaults or claiming support. Preserve explicit saved/request pins; don't infer feature parity from a version number or relabel a lagging route as latest. Keep Clef distinct from Jev.
- Preserve locking, private atomic writes, managed-skill ownership checks, and unrelated client settings during installation. Don't install agent configuration, connect accounts, or change real credentials as a side effect of builds or tests.

## Verification

Tests are not required for every change. Run relevant tests when requested or when the change warrants them. Prefer behavioral tests with temporary state, local fake APIs, and real in-memory or stdio MCP transports. Cover invalid input, malformed responses, cancellation, and failure behavior, not just successful requests. Tests must not require live credentials or paid calls.

For code changes, format affected Go files. Linting is mandatory. After the final edits, run and verify:

```sh
make fmt-check
make lint
make vet
make build
```

Use `make check` for vet and race tests. For platform-sensitive or release changes, run `make release-local` to build and verify all six platform bundles. Cross-compilation does not prove runtime behavior. `make ci` runs the full local CI checks, including lint, race tests, vulnerability scanning, builds, release verification, dependency verification, and workflow linting.

Fix failures caused by the changes and rerun affected checks. Report commands and results in the final response. State blocked checks, unrelated failures, and untested live-provider behavior explicitly; don't claim verification that did not run.

For documentation-only changes, check relative links and confirm described behavior against the implementation. A full Go test run is unnecessary. Validate changed JSON/JSONL examples and use local `--dry-run` checks for CLI payload examples where practical. Check shell installer syntax with `sh -n scripts/install.sh` when editing it.

## Documentation and release maintenance

- Keep `AGENTS.md` about maintaining this repository, not end-user task execution.
- Keep `skills/decide/SKILL.md` about CLI scenarios, tasks, payloads, answer interpretation, and Jev behavior. Don't add provider/model catalogs, credentials, setup instructions, release infrastructure, or MCP-specific sections. Preserve its `name: decide` frontmatter and managed marker; rebuild the binary after changing embedded guidance.
- Keep provider setup, current versions, capabilities, caveats, and dated upstream references in `docs/providers.md`. Keep MCP setup, schemas, client paths, and installation behavior in `docs/agents.md`.
- Use `docs/architecture.md` for implementation flow, `docs/design.md` for choices and tradeoffs, and `docs/workflows.md` for operational workflow contracts. Update examples, CLI help, MCP descriptions, and README links when their contracts change.
- Preserve six-platform CGO-free release bundles, manifests, and verified checksums. Keep CI tools and actions pinned. Release metadata is signed in CI; installers verify checksums, so don't claim installer signature verification unless it is implemented.
- Keep build tooling separate from the installed command. `make install` installs only `decide`; a build or documentation change must not publish a release or alter a user's installed integrations.
