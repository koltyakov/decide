# Design decisions

## Use Jev's vocabulary

The public primitives are `noul`, `choice`, `score`, and `box`. Shared requests use `systemone`, matching the API endpoint. Inputs use `state`, `instructions`, `criteria`, `questions`, and `assets`. Results retain native answer fields.

Renaming these to generic verbs would make the CLI harder to relate to the API documentation. Distinct workflow behavior does not require a different name for the same primitive.

## Separate literal values from file reads

`--state` always means a literal value. `--state-file` explicitly reads a file or stdin. Instructions and criteria have corresponding file flags. A leading `@` in data never opens a file. This removes a class of surprising reads and keeps CLI and MCP literal-input semantics aligned.

## Treat connection setup as a lifecycle

`providers connect` verifies before saving; `use` changes the default explicitly; `status` only reads; `test` verifies; `disconnect` removes local connection material. Official verification uses model discovery rather than spending decision tokens. Settings, first default, and entered keys are written in one atomic update.

Each provider has isolated credentials. A process override takes precedence over saved configuration. Agent tool arguments cannot redirect an authenticated request to another endpoint.

## Track current Jev without inventing provider parity

Prefer documented stable aliases where available and explicit current pins otherwise. The current stable official release is 1.14.0. Saved and request-level model pins stay fixed; preview releases require an explicit override. A lagging provider is labeled as such, not assigned an invented latest ID. Clef has its own provider identity because it is not Jev.

Capabilities are provider- and model-specific. Unsupported images, Box, and opt-in headers fail before a request. OpenRouter assets are converted into its documented image parts because forwarding a TypeSafe asset map would silently lose the images. This conversion is bounded before encoding. Catalog discovery retains native response shapes and is not used as proof of authentication when the catalog is public.

## Make workflow decisions explicit

Gates are caller-supplied rules, not hard-coded recommendations. Valid rejection has its own exit code and metadata, distinct from a failed API call. Missing confidence fails closed. Gates do not grant authorization or execute an action.

Offline preflight and execution use the same encoder and validation. Native request recipes can be reused with new state. JSONL batching bounds concurrent work, preserves ordering, and reports errors per record without retrying uncertain requests.

Request compression is explicit and provider-specific. It reduces transferred bytes, not the native request limit. Keep compressed and uncompressed bodies bounded, and fail instead of replaying a request with a different encoding.

## Make agent discovery useful without inference

Publish task-focused skill metadata and MCP tool descriptions, schemas, and annotations. Keep discovery offline unless the caller explicitly requests a model catalog. Local capabilities distinguish published support from process permissions and never claim live verification. Expose the embedded CLI skill as an MCP resource without requiring hosts to install it. Input schemas are enforced, not just advertised; native extension fields remain inside the complete request rather than becoming process configuration.

## Keep the executable self-contained

CLI and MCP share one Go client and workflow implementation. Skills are embedded. There is no Python runtime, subprocess adapter, daemon, or dependency on the reference project. Control's integration and Go release conventions are reused where they fit, without its distributed execution machinery.
