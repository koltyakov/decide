# Workflow controls

Jev request and response fields retain their API meanings. These features are client-side controls around a native System One call.

## Offline preflight

Every primitive, `systemone`, and `batch` accepts `--dry-run`. Preflight uses the same request and image encoder as execution. It resolves provider settings, validates questions and policies, and reports the actual encoded body size without a network call or key requirement.

```sh
decide systemone examples/triage.json --dry-run
decide batch examples/triage.json --states examples/states.jsonl --dry-run
```

Preview fields include `provider`, `endpoint`, `model`, `request`, `headers`, `bodyBytes`, `uncompressedBodyBytes`, `compression`, `uploads`, and `policy`. `bodyBytes` is the transmitted size after optional compression; `uncompressedBodyBytes` includes the encoded JSON or multipart framing before compression. The request remains a readable JSON object, not compressed bytes. Headers omit authorization. Upload metadata contains reference, format, and bytes, not file contents. Inline base64 assets remain in the preview's request. Supplied state and instructions are printed, so don't send previews to public logs.

Official/custom calls accept `--compression gzip`, `--compression x-gzip`, or `--compression zstd`; the default is `none`. Preflight and execution use the same compressed body. Both sizes must fit 32 MiB. Compression cannot bypass native limits and never enables an automatic fallback or retry. Batches reuse the invocation's compression setting independently for each record.

A successful preflight is not a guarantee of server acceptance, model availability, context fit, or accuracy. Processed-image expansion and token budgets remain server checks. Nothing is uploaded during preflight.

## Acceptance policy

Policies are optional. No default threshold is applied. A policy selects one question and combines its rules with AND:

| CLI flag | MCP policy field | Applies to |
| --- | --- | --- |
| `--answer NAME` | `answer` | Explicit question selection; required with multiple questions |
| `--at-least N` | `atLeast` | Noul probability or Score level |
| `--is KEY` | `is` | A declared Choice option |
| `--min-confidence N` | `minConfidence` | Choice, Score, or Box confidence |

Confidence is not probability. Noul has no confidence gate; use its probability. A confidence gate fails when the value is missing, null, non-numeric, non-finite, or outside 0 to 1. This remains true for `minConfidence: 0`.

Thresholds must fit the question's range. A score with three levels supports 0 to 2; a Noul supports 0 to 1. An unknown Choice key, a missing question selection, or a policy for the wrong primitive fails before an API call.

The native response is unchanged except for explicit client metadata when a gate is requested:

```json
{
  "model": "jev-latest",
  "answers": {"urgent": {"type": "noul", "noul": 0.8}},
  "_decide": {
    "policy": {"answer": "urgent", "atLeast": 0.9},
    "gate": {
      "answer": "urgent",
      "passed": false,
      "checks": [{"rule": "atLeast", "passed": false}]
    }
  }
}
```

CLI exit status is 5 for this valid rejection. There is no extra error on stderr. MCP returns this as a successful tool result, not a tool error. Invalid requests and provider failures still use their normal error channels.

`--output value` extracts the primitive value and retains gate exit behavior. With multiple questions, specify `--answer`. `--output raw` bypasses client metadata and cannot be combined with acceptance rules. JSON output preserves native answer keys; it does not replace them with a universal value field.

In shell code, distinguish exit 5 from other nonzero codes if an API failure must not be treated as an ordinary rejected decision. Decide does not run shell commands or change permissions based on an answer.

## Reusable native requests

`systemone` accepts the API's JSON request. `--state`, `--state-file`, and `--state-format` can replace its state while preserving questions, assets, and extension fields:

```sh
decide systemone examples/triage.json --state-file ticket.json --state-format json
```

A recipe may omit state only when a caller provides it. A request file with state works unchanged. The same recipe can be used by `batch`, where every JSONL row replaces state.

Model precedence is explicit `--model`, request `model`, custom-provider `DECIDE_MODEL`, saved connection model, provider default. Choice options and score criteria retain their declared structure and descriptions. No question DSL or template interpolation is added.

## JSONL batches

```sh
decide batch examples/triage.json --states examples/states.jsonl --jobs 4
```

Each non-empty line is a state string, object, or array. Scalars such as numbers and booleans are invalid native states and produce record errors. Local paths in records stay text. Batches don't interpret JSON members as filenames.

Each output line corresponds to one non-empty input line in the same order:

```jsonl
{"index":0,"ok":true,"response":{"answers":{"urgent":{"noul":0.8}}}}
{"index":1,"ok":false,"error":"API HTTP 429; not retried","code":4}
```

The command processes independent records after per-record errors. It reports all results, then exits 6 if any records failed, otherwise 5 if any gates were rejected, otherwise 0. A rejected record has `ok: true`, a valid response, and `code: 5`. No error is silently converted into a decision.

The default `--jobs 1` makes request spending serial and explicit. Values up to 16 use fixed-size concurrent windows. Every window drains before the next starts, so a slow early request cannot build an unbounded reorder queue. This trades some throughput under uneven latency for bounded memory and predictable ordering. Each line is limited to 32 MiB; memory grows with window size and request bodies, not the entire input stream. Output is compact JSONL; `--pretty` and non-JSON output are rejected.

Cancellation stops later windows and reaches in-flight HTTP requests. Completed calls can still be billed; no interrupted request is automatically retried. An unreadable or oversized input line stops scanning with an input error, after any already processed rows.

Batch supports native inline assets in the recipe and state references in each row. It does not accept file-attachment flags or read per-row image paths. Use a normal multipart `systemone` call for local image files, or construct native inline asset recipes for batch use.
