# Providers and Jev versions

Contracts and version references checked on October 26, 2026. The current official stable Jev release is **1.14.0**, released October 14, 2026. Image input and Box remain experimental. Newest model availability and feature availability are separate facts: an API gateway can expose Jev 1.14 without exposing every TypeSafe request feature.

## Version policy

| Provider | Default | Explicit stable pin | Published scope |
| --- | --- | --- | --- |
| TypeSafe official | `jev-latest` | `jev-1.14.0` | Current stable; images, Box, long-context opt-in |
| OpenRouter | `~typesafe/jev-latest` | `typesafe/jev-1.14` | Current Jev; images and Box through Decisions |
| Vercel AI Gateway | `typesafe-ai/jev-1.14.0` | Same | Explicit current model route; image/Box integration not documented for this adapter |
| AI/ML API | `typesafe/jev` | `typesafe/jev-1.14` | Latest alias; native inline assets and Box |
| DigitalOcean | `typesafe-jev-1.13.0` | Same | Its current catalog still lists 1.13; text/JSON only |
| Cloudflare Jev | `typesafe/jev` | No versioned route documented | Unversioned route; examples resolve to 1.13; text/JSON contract |

Prefer official, OpenRouter, or AI/ML API for the current full Jev image/Box contract. The CLI does not invent newer model IDs for lagging providers, silently downgrade after an error, or change provider credentials as a fallback.

TypeSafe's `jev-preview` currently points to 1.14.0 too, but can advance ahead of stable. Select it explicitly with `--model jev-preview`; it is not the default. Aliases follow upstream updates without a CLI release, whereas explicit pins stay fixed. Log the response's `model`, and retune acceptance thresholds before changing a pin. Saved connection models and request models are not overwritten when built-in defaults change.

```sh
decide providers connect official --model jev-1.14.0
decide providers connect openrouter --model typesafe/jev-1.14
decide providers connect aimlapi --model typesafe/jev-1.14
decide noul --provider official --model jev-preview --instructions 'Urgent?' --state 'Please fix today.'
```

## Connection checks and discovery

All providers work through `decide providers connect/use/status/test/disconnect`. `list` and `status` report `family`, model settings, key source, and `capabilities`. `family: clef` is deliberately distinct from `family: jev`. Capabilities describe TypeSafe-style assets and opt-in headers, not unrelated media APIs.

- Official and DigitalOcean connection checks use an authenticated models GET. This tests catalog access, not inference availability for every model or account tier.
- Other connection checks use a minimal billable Noul request, including OpenRouter. Its model catalog is public, so reading it alone must not be treated as key verification.
- `decide models` supports official/custom catalogs with `models[].name`, DigitalOcean with `data[].id`, and OpenRouter's Decisions catalog with `data[].id`. Responses remain native rather than being recast into one catalog schema.
- OpenRouter discovery requests `output_modalities=decisions`; the ordinary chat-model listing excludes Jev decision models.
- Discovery may list aliases rather than every accepted version pin. Use documented version identifiers when reproducibility matters.
- `decide capabilities` and the MCP `capabilities` tool inspect local provider/model capabilities and process permissions without a key or network call. They never verify live access. The MCP `models` tool exposes the same explicit catalog GET as `decide models`.

## Request compression

Official/custom routes support `--compression none|gzip|x-gzip|zstd`, default `none`. Both JSON and multipart bodies use one compression layer after validation and encoding. The client checks the original and compressed body against 32 MiB and limits zstd's window to 8 MiB. Compression does not increase image, expanded-request, or context limits. Custom proxies must implement the TypeSafe compression contract themselves.

Other gateway contracts do not establish request compression support, so the client rejects non-`none` compression before HTTP on those routes. No compression failure triggers a retry, fallback encoding, or provider switch. Catalog GETs are not compressed. MCP compression is set on the server process, not by tool arguments.

Source: [TypeSafe request compression](https://docs.typesafe.ai/api/errors.md#compression).

## DigitalOcean

Endpoint: `https://inference.do-ai.run/v1/systemone`.

Use a Serverless Inference **model access key**, stored with `decide providers connect digitalocean` or supplied through `DIGITALOCEAN_MODEL_ACCESS_KEY`. The CLI uses this dedicated name to avoid accidentally picking up a broader account-management token.

Requests and responses use native System One JSON. Model discovery uses `https://inference.do-ai.run/v1/models`. Some subscription tiers cannot invoke Jev even if the key can access the catalog. Its published Jev contract is text/JSON, Noul/Choice/Score, no image/Box or TypeSafe long-context opt-in.

Sources: [System One API](https://docs.digitalocean.com/products/inference/how-to/use-system-one-api/), [available models](https://docs.digitalocean.com/products/inference/details/models/), [model discovery](https://docs.digitalocean.com/products/inference/how-to/retrieve-available-models/).

## Cloudflare Jev

Provider: `cloudflare`. Routing model: `typesafe/jev`.

Requires `CLOUDFLARE_ACCOUNT_ID`, 32 hexadecimal characters, and a Workers AI token from `CLOUDFLARE_API_TOKEN` or the saved provider key. The REST endpoint is `https://api.cloudflare.com/client/v4/accounts/ACCOUNT/ai/run`.

The adapter turns the native request into `{"model":"typesafe/jev","input":<native state/questions>}`. It doesn't copy the routing model into the inner Jev model field. The catalog shows a native decision response; Workers AI can also return `success`/`result` REST envelopes. Both are supported, but explicit failure, missing envelope success, or malformed results never fall back to apparent answers. Error messages from the service are not reflected.

The documented route exposes Noul/Choice/Score, not TypeSafe image assets, Box, or the TypeSafe long-context header. Do not infer 1.14 support from its unversioned name.

Source: [Cloudflare Jev](https://developers.cloudflare.com/ai/models/typesafe/jev/).

### Clef is a separate model family

`--provider clef` retains the Cloudflare Clef adapter. Its default is `clef-flash`; `--model clef` chooses the larger model. Endpoints include the model path, `/ai/run/@cf/cloudflare/clef-flash` or `/ai/run/@cf/cloudflare/clef`, and responses must be successful Workers AI envelopes.

Clef is **not TypeSafe Jev**. Its token environment variable and account ID are shared with Cloudflare Jev, but saved provider keys/settings remain isolated. Its native `images` extension may be sent through a raw System One request; this CLI does not translate TypeSafe assets into Clef media.

Earlier development builds called the Clef adapter `cloudflare`. Reconnect explicitly as `clef` to keep using it. Saved Cloudflare settings with an explicit Clef model or endpoint are rejected with a migration hint; they are not silently reinterpreted as Jev or rewritten.

Source: [Clef Flash](https://developers.cloudflare.com/workers-ai/models/clef-flash/).

## AI/ML API

Provider: `aimlapi`. Endpoint: `https://api.aimlapi.com/v1/decisions`. Key: `AIMLAPI_KEY` or the saved provider key.

The default `typesafe/jev` alias currently resolves to 1.14. Use `typesafe/jev-1.14` to pin it. Native question fields and response metadata, including `meta`, are preserved. This API requires instructions on each question and at least two Score levels; these constraints are checked locally.

Images use native `assets` with base64, not multipart. Local `--image` files are converted to inline assets, with the same generated references or explicit request references as other CLI calls. No TypeSafe opt-in header is added, but the CLI still requires `--experimental-images` to authorize the experimental feature. Its explicit 1.13 model pin disables Box. The provider documents a 262,144-token context without the TypeSafe long-context header.

Sources: [Jev 1.14 API](https://docs.aimlapi.com/api-references/decision-models/typesafe/jev-1.14), [model catalog](https://aimlapi.com/providers/typesafe-ai).

## OpenRouter

Provider: `openrouter`. Endpoint: `https://openrouter.ai/api/alpha/decisions`. Default: `~typesafe/jev-latest`; stable pin: `typesafe/jev-1.14`. Key: `OPENROUTER_API_KEY`.

TypeSafe-style `assets` are currently ignored by the Decisions API. The CLI therefore replaces each referenced image with OpenRouter's native `image_url` part and removes the asset map. Replacement works in state, structured instructions, and criterion values. A reference embedded in a string becomes an array of text/image parts in reading order. Whole-string references take precedence; object keys are never replaced.

Direct native parts in state/instructions/criteria are supported too. They must contain inline PNG/JPEG data URLs; remote URLs are rejected and never fetched. Native parts cannot bypass opt-in, image validation, dimensions, or byte limits. Each occurrence counts toward a bounded image-reference expansion budget before encoding, so a repeated large image cannot create unbounded output.

Images and Box are experimental and need `--experimental-images`. Box is disabled for explicit 1.13 pins. OpenRouter controls long-context mode automatically, including for images; the CLI doesn't send TypeSafe's long-context header. Cost and provenance fields such as `usage.cost`, `provider`, and `id` are preserved.

Sources: [Jev guide and current IDs](https://openrouter.ai/docs/guides/community/jev), [multimodal Decisions contract](https://openrouter.ai/docs/guides/community/multimodal-decisions).

## Vercel, official, and custom

Vercel uses the explicit current `typesafe-ai/jev-1.14.0` model in the `ai-model-id` header. Native Noul questions are translated to its boolean evaluation type and normalized back to Noul answers. The generic `typesafe-ai/jev` alias includes a DigitalOcean route too; the explicit current pin avoids assuming that older route is already 1.14. Image/Box request handling is not inferred from model-version availability.

The official default is `jev-latest`, currently `jev-1.14.0`. It accepts native inline assets, multipart files, and the experimental images and long-context headers. `custom` assumes this native contract at a user-supplied trusted endpoint; the actual proxy must implement it. Model IDs are not translated between providers automatically.

Sources: [TypeSafe models](https://docs.typesafe.ai/models.md), [Vercel Jev 1.14.0](https://vercel.com/ai-gateway/models/jev-1.14.0), [Vercel generic Jev route](https://vercel.com/ai-gateway/models/jev).
