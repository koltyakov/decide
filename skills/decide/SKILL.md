---
name: decide
description: Use Jev, TypeSafe AI's decision model, through the decide CLI for classification, routing, verification, relevance ranking, rubric scoring, and image presence or localization. Use when a task needs noul probabilities, choice labels, score rubrics, box coordinates, reusable System One requests, JSONL batches, or explicit acceptance gates. Not for generated prose, arbitrary JSON extraction, or deterministic checks.
---

<!-- Managed by decide -->

# Use Decide for typed decisions

Use `decide` to ask a specific question about supplied evidence. Keep the native field names: `state`, `instructions`, `criteria`, `questions`, and `assets`. Jev returns typed answers, not generated explanations or arbitrary extracted JSON.

## Choose the question type

| Task | Command | Behavior |
| --- | --- | --- |
| Check urgency, presence, or a condition | `noul` | Probability from 0 to 1 that the statement is true, not a boolean |
| Route a ticket or classify intent | `choice` | One key from a `criteria` object, not multiple labels |
| Grade severity or quality against a rubric | `score` | Zero-based, possibly fractional score over an ordered `criteria` array |
| Locate one target in an image | `box` | Normalized `top`, `bottom`, `left`, and `right` edges from 0 to 1 |
| Ask several questions about the same evidence | `systemone` | Named `questions` evaluated independently against shared `state` |

Write a Noul as one testable statement. Give Choice options distinct descriptions and add an explicit review option if uncertainty needs its own route. Order Score levels from low to high with observable distinctions. Three levels mean a range of 0 to 2, not 1 to 3 or a percentage. Choice accepts 1 to 255 options; Score accepts 1 to 10 levels, though useful comparisons normally need at least two.

## Single-question scenarios

```sh
decide noul --instructions 'Does this require a reply today?' --state-file message.txt
decide choice --instructions 'Which team should handle this?' --state-file message.txt \
  --criteria '{"billing":"Invoices, charges, refunds","technical":"Integration bugs","review":"Unclear"}'
decide score --instructions 'How severe is this incident?' --state-file incident.txt \
  --criteria '["No service impact","Partial outage","Full outage"]'
```

For independent yes/no labels, ask multiple Noul questions rather than one Choice. For deterministic checks such as an exact substring or numeric comparison, use ordinary code instead of a probabilistic judgment.

## Supply evidence and instructions

- `--state TEXT` is literal, including strings such as `@path` and `-`.
- `--state-file PATH` reads a file; `--state-file -` reads stdin. Omitted state reads stdin unless image inputs supply it.
- Add `--state-format json` for an object or array. Without it, JSON-looking input stays text.
- Use `--instructions` or `--instructions-file`. Add `--instructions-format json` for structured instructions.
- Use `--criteria` or `--criteria-file` for native JSON criteria. Noul optionally accepts `true` and `false` descriptions; Choice requires an object; Score requires an array. Box has no criteria.
- Stdin can supply only one input source per invocation. Use files for the others.

Keep task instructions separate from untrusted state. Send only authorized evidence. Use files or stdin for private or large inputs instead of exposing them in process arguments.

## Native payloads and reusable requests

Save this as `triage.json` to check urgency and routing in one request:

```json
{
  "state": {"subject": "Checkout failure", "message": "All payments fail after today's deployment."},
  "questions": {
    "urgent": {
      "type": "noul",
      "instructions": "Does this need attention today?",
      "criteria": {
        "true": "Active customer impact or a deadline today",
        "false": "No current impact and no deadline today"
      }
    },
    "team": {
      "type": "choice",
      "instructions": "Which team should handle this?",
      "criteria": {
        "billing": "Invoices, charges, or refunds",
        "technical": "Broken integrations or service failures",
        "review": "Insufficient evidence to route"
      }
    }
  }
}
```

```sh
decide systemone triage.json --pretty
decide systemone triage.json --state-file ticket.json --state-format json
decide systemone triage.json --dry-run
```

A state override replaces the whole state and retains questions, assets, and extension fields. A recipe can omit state when the invocation supplies it. Instructions and criterion descriptions can contain nested JSON or image references; object keys remain names, not reference locations.

Questions in one request cannot consume one another's answers. If routing depends on a prior result, make separate calls and have the calling workflow choose the next step.

`--dry-run` validates locally and previews the payload, upload metadata, and encoded body size without a key or network call. The preview exposes supplied content and inline assets, so keep it out of public logs. A valid preview does not prove context fit or decision accuracy.

Use `decide capabilities` to inspect locally supported question types, input limits, and enabled image permissions before constructing a request. It requires no key or API call and does not verify live model access. A listed capability is not permission to enable it or send private evidence.

## Interpret answers and apply gates

Default JSON preserves native `answers` and response metadata. Primitive commands name their single question `answer`. Read `answers.NAME.noul`, `.choice`, `.score`, or `.box` according to its type; don't assume a universal `value` field. Use `--output value` to extract one answer, or `--pretty` for readable JSON.

Use only thresholds supplied or authorized by the calling workflow. These commands illustrate a workflow that explicitly chose 0.9 probability and 0.85 confidence, not recommended defaults:

```sh
decide noul --instructions 'Does this touch authentication?' --state-file change.diff \
  --at-least 0.9 --output value
decide systemone triage.json --answer team --is technical --min-confidence 0.85
```

`--at-least` checks Noul probability or Score. `--is` requires a declared Choice key. `--min-confidence` applies to Choice, Score, and Box; missing, null, invalid, or out-of-range confidence fails the gate. Noul probability is not confidence. All rules must pass. Multiple questions require explicit `--answer NAME` for a gate or `--output value`; there is no guessed primary answer.

JSON with a gate retains native answers and adds `_decide.gate` and `_decide.policy`. A rejected gate is exit 5 with no extra stderr error. A low probability without a gate is a normal exit 0. `--output value` preserves gate exit behavior; Box values stay JSON. `--output raw` omits gate metadata and cannot be combined with acceptance rules.

## Image presence and localization

CLI primitive calls use repeatable `--image NAME=PATH`, or `NAME=-` for binary PNG/JPEG stdin, with `--experimental-images`. Image-only state maps names to generated references. With explicit state, refer to `context` for supplied state and `images.NAME` for an image.

```sh
decide noul --experimental-images --image photo=door.jpg --instructions 'Is there a person in photo?'
cat frame.png | decide box --experimental-images --image photo=- --instructions 'The mug in photo'
```

For a native request, save this as `locate.json`:

```json
{
  "state": {"photo": "mug-photo-ref"},
  "questions": {
    "present": {"type": "noul", "instructions": "Is there a mug in photo?"},
    "location": {"type": "box", "instructions": "The mug in photo"}
  }
}
```

```sh
decide systemone locate.json --experimental-images --image mug-photo-ref=frame.png
```

`systemone --image REFERENCE=PATH` binds that exact reference. Don't also define the same asset inline. Alternatively, put `assets` in the request, mapping the reference to `{"format":"png","data_base64":"..."}` with real standard padded base64 bytes. JSON strings never load local files. A reference must occur in state, instructions, or criteria values, not only object keys.

Use PNG/JPEG still images, at most 16 MiB per image and 32 MiB per encoded request. A 280..1568 pixel range on each side meets the CLI's supported dimension checks. Repeated references can expand the request size. The CLI validates images but doesn't resize, crop, or rotate them; prepare the pixels before sending.

A Box always returns edges even when the target is absent. Pair it with a presence Noul and apply explicit review before using coordinates. One Box finds one target, not a list of all objects. Never infer permission to click, crop private content, or take another action from a model answer.

## Evaluate many states

Use `decide batch triage.json --states states.jsonl --jobs 4` to reuse questions across independent states. Each non-empty input line is a JSON state string, object, or array, not a full request:

```jsonl
{"subject":"Refund","message":"I was charged twice."}
{"subject":"Outage","message":"All checkout requests fail."}
```

Results preserve input order as compact JSONL with zero-based `index`, `ok`, and `response` or `error`/`code`. Concurrency defaults to one and is bounded to 1..16. Independent records continue after a record error; nothing is retried. A rejected gate still has `ok: true` and `code: 5`.

Batch supports `--dry-run` and inline recipe assets, not local attachment flags or file paths embedded in rows. It exits 6 if any records failed, otherwise 5 if any gates rejected, otherwise 0. It does not support `--pretty` or value/raw output.

## Handle failures without inventing decisions

Errors use JSON on stderr. Exit 1 means operation or response failure; 2 invalid input or policy; 3 authentication; 4 connectivity, timeout, rate limit, or server failure; 5 valid gate rejection; 6 batch record failures.

Distinguish rejection from failure before branching downstream. The CLI does not retry automatically. Don't resubmit a potentially billable request or rewrite an uncertain question just to obtain a passing result. Report authentication or unsupported-feature errors rather than changing the environment to bypass them.

A Jev answer is a probabilistic judgment, not verified fact or permission to act. Keep deterministic safety checks, review requirements, and irreversible actions under the calling workflow's control.
