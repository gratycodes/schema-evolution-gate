# schema-evolution-gate

Fail the pull request, not the consumer. `schema-evolution-gate` is a Go CLI and GitHub Action that checks Kafka/event schema changes (Avro and JSON Schema) in a PR diff with Confluent Schema Registry compatibility rules, without a running registry, and replays recorded sample payloads against the proposed schema.

## The problem

Event schemas are a contract between teams that deploy on different days. Martin Kleppmann's [Schema evolution in Avro, Protocol Buffers and Thrift](https://martin.kleppmann.com/2012/12/05/schema-evolution-in-avro-protocol-buffers-thrift.html) explains why this works at all: Avro readers resolve data written with an older (or newer) writer schema through resolution rules, so producers and consumers can upgrade independently, as long as every change follows those rules. Yelp's [More Than Just a Schema Store](https://engineeringblog.yelp.com/2016/08/more-than-just-a-schema-store.html) describes running that discipline at scale: their Schematizer used Avro resolution rules to decide whether a new schema could share a Kafka topic with existing ones, so consumers could "continue reading old messages" through upstream changes.

Most teams get that check from Schema Registry, which means they find out at **producer deploy time** (registration rejected) or, with auto-registration in a lax mode, at **consumer read time**. The schema file was sitting in a pull request the whole time. This tool moves the check to review time:

- the PR's schema is compared with the base branch (git merge-base, like the PR diff) and with any older versions kept in the repo;
- each breaking change comes with the rule name, the field path, the direction (backward or forward) and a plain-language reason;
- real-looking payloads from the repo are decoded with the proposed schema, which catches breaks that compatibility rules allow (for example a lenient JSON policy, or a type change hidden in an open content model);
- the Action posts one PR comment and keeps updating it, and fails the check on breaking changes.

## Quick start

```yaml
# .schema-gate.yaml
version: 1
defaults:
  mode: BACKWARD            # BACKWARD (default), FORWARD, FULL, *_TRANSITIVE, NONE
  jsonPolicy: strict        # strict (Confluent default) or lenient
  sampleEncoding: avro-json # for Avro samples: avro-json (unions wrapped) or json
topics:
  - topic: orders
    schema: schemas/orders/order-placed.avsc
    mode: FULL_TRANSITIVE
    history: schemas/orders/history/order-placed.v*.avsc  # older registered versions, natural-sorted
    samples: samples/orders                                # *.json, *.jsonl or *.ndjson
  - topic: payments
    subject: payments-value   # default: <topic>-value, used in reports
    schema: schemas/payments/payment-captured.schema.json
    jsonPolicy: lenient
    samples: samples/payments
```

```yaml
# .github/workflows/schemas.yml
on: pull_request
permissions:
  contents: read
  pull-requests: write
jobs:
  schema-gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0   # the base branch must be available for git
      - uses: gratycodes/schema-evolution-gate@main
```

A ready-to-copy workflow and config are in [`examples/`](examples).

Action inputs: `config`, `base-ref` (default `origin/<PR base branch>`), `working-directory`, `fail-on-breaking` (default `true`), `comment` (default `true`), `extra-args` (for example `--all`), `github-token`. Outputs: `breaking`, `failed-topics`, `report-json`, `report-markdown`. The report is also written to the job summary. On fork PRs the token is read-only, so the comment is skipped with a warning and the check result still applies.

### CLI

```
go install github.com/gratycodes/schema-evolution-gate/cmd/schema-evolution-gate@latest

schema-evolution-gate check   [--config .schema-gate.yaml] [--base origin/main] [--output table|json|markdown]
                              [--markdown-out FILE] [--json-out FILE] [--all] [--no-merge-base]
schema-evolution-gate compare --old FILE [--old FILE ...] --new FILE [--mode BACKWARD] [--schema-format avro|json]
                              [--json-policy strict|lenient] [--output table|json|markdown]
schema-evolution-gate replay  --schema FILE [--base FILE] --samples DIR [--schema-format avro|json]
                              [--encoding avro-json|json]
```

Exit codes: `0` compatible, `1` breaking changes, failed replays or invalid schemas, `2` usage or setup error. `compare` takes `--old` oldest first. Formats are detected from the extension (`.avsc`, `.json`), or set with `format:` / `--schema-format`.

## What gets compared

For each topic in the config:

1. **Versions.** The history files (oldest to newest), then the schema at the merge base of `--base` and `HEAD` if it differs from the newest history file. Without git, or when the file is new at base, only the history files are used; no versions at all means the topic is reported as a new subject.
2. **Mode.** Non-transitive modes check the newest previous version only; `*_TRANSITIVE` modes check all of them. The same break against several versions is reported once (`against: base, order-placed.v1.avsc`).
3. **Directions.** `backward`: a consumer on the new schema reads events written with the old one (old data on the topic, replays, rewinds). `forward`: a consumer still on the old schema reads events from the new producer. `FULL` checks both.
4. **Replay.** Samples are read with the base schema as writer and the proposed schema as reader (Avro), or validated against the proposed schema (JSON Schema). Samples that were already invalid under the base schema are reported as stale, not as failures.

Unchanged schemas are skipped unless `--all` is set.

## Rules

Avro follows the Avro specification's schema resolution, using the incompatibility types of Apache Avro's `SchemaCompatibility`, which is what Schema Registry reports for Avro.

| Rule | Example that breaks | Direction that breaks |
|---|---|---|
| `READER_FIELD_MISSING_DEFAULT_VALUE` | add a field without `default` | backward (for removal: forward) |
| `TYPE_MISMATCH` | `int` → `string`; `long` → `int` (no narrowing promotion) | the direction without a promotion |
| `MISSING_ENUM_SYMBOLS` | remove an enum symbol, with no enum `default` | backward (adding one: forward) |
| `MISSING_UNION_BRANCH` | drop a union branch, or narrow a field to a union that lacks the writer's type | backward |
| `NAME_MISMATCH` | rename a record, enum or fixed without an alias | both |
| `FIXED_SIZE_MISMATCH` | change a `fixed` size | both |

Allowed promotions: `int` → `long`, `float`, `double`; `long` → `float`, `double`; `float` → `double`; `string` ↔ `bytes`. Fields match by name or reader alias, named types by unqualified name or alias.

JSON Schema follows Confluent's JSON Schema compatibility model: a structural diff that yields change types such as `TYPE_NARROWED`, `REQUIRED_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL`, `PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL`, `MAX_LENGTH_DECREASED`, `ENUM_ARRAY_NARROWED`, `SUM_TYPE_NARROWED`, and so on, each classified as compatible or not. The content model matters:

| Change | Open model (`additionalProperties` absent/true) | Closed model (`additionalProperties: false`) |
|---|---|---|
| add optional property | breaks backward under `strict` (old events may already carry that name with any type); allowed under `lenient` | backward OK; breaks forward (old consumers reject the new property) |
| remove property | backward OK | breaks backward |
| add required property | breaks backward | breaks backward |
| narrow a type, tighten `maxLength`/`maximum`/`enum` | breaks backward | breaks backward |

`strict` is Confluent's default policy. `lenient` is what many teams turn on to make "add an optional field" pass; replay is how this tool keeps lenient honest (see `payments` in the sample below).

## Sample PR comment

This is the real output for the synthetic fixture in [`testdata/fixture`](testdata/fixture) (the CI self-test runs the Action on it). The `orders` change adds `currency` without a default, removes the `REFUNDED` status and widens `quantity` from `int` to `long`; `payments` adds `risk_score: integer` to an open model under the lenient policy, which passes the rules, but one recorded payload has `"risk_score": "LOW"`.

<details open><summary>Rendered comment</summary>

<!-- schema-evolution-gate -->
### ❌ Schema evolution gate failed: 3 breaking changes, 2 of 4 topics failing

Compared with `main (3f9c1e2a7b40)`.

| Topic | Subject | Format | Mode | Result |
|---|---|---|---|---|
| `orders` | `orders-value` | Avro | `FULL_TRANSITIVE` | ❌ 3 breaking, replay 4/4 failed |
| `payments` | `payments-value` | JSON Schema | `BACKWARD` | ❌ compatible by rules, replay 1/3 failed |
| `refunds` | `refunds-value` | Avro | `BACKWARD` | ➖ unchanged |
| `customers` | `customers-value` | JSON Schema | `BACKWARD` | ✅ compatible (1 non-breaking change) |

#### `orders-value` (`schemas/orders/order-placed.avsc`)

| Rule | Field | Direction | Against | Why |
|---|---|---|---|---|
| `READER_FIELD_MISSING_DEFAULT_VALUE` | `OrderPlaced.currency` | backward | `base, order-placed.v1.avsc` | new field `currency` has no default, so the new schema cannot read older events that lack it |
| `MISSING_ENUM_SYMBOLS` | `OrderPlaced.status` | backward | `base, order-placed.v1.avsc` | enum symbol removed from `status`; older events that use it cannot be read |
| `TYPE_MISMATCH` | `OrderPlaced.lines[].quantity` | forward | `base, order-placed.v1.avsc` | type of `lines[].quantity` changed: data written as long cannot be read as int |

**Replay:** 4 of 4 recorded payloads fail against the new schema.

| Sample | Path | Error |
|---|---|---|
| `order-placed.jsonl:1` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:2` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:3` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:3` | `/status` | enum symbol "REFUNDED" no longer exists in shop.orders.OrderStatus |
| `order-placed.jsonl:4` | `/currency` | field "currency" is not in the payload and has no default |

#### `payments-value` (`schemas/payments/payment-captured.schema.json`)

**Replay:** 1 of 3 recorded payloads fail against the new schema.

| Sample | Path | Error |
|---|---|---|
| `payment-captured.jsonl:2` | `/risk_score` | got string, want integer |

<details><summary>How to read this</summary>

- **backward**: consumers on the new schema reading events written with an older version. Breaks here mean old events on the topic (retention, replays, rewinds) become unreadable.
- **forward**: consumers still on an older schema reading events from the new one. Breaks here mean producers cannot ship before every consumer upgrades.
- Rules follow Confluent Schema Registry compatibility semantics (Avro resolution rules; JSON Schema diff types and policy). Replay validates recorded payloads from the repo against the proposed schema.
</details>

<sub>schema-evolution-gate v0.1.0</sub>

</details>

## Limitations

- **No Protobuf.** Use [`buf breaking`](https://buf.build/docs/breaking/), which is the right tool for it.
- **JSON Schema coverage.** The diff covers the keywords in Confluent's model (types, properties, required, `additionalProperties`/`patternProperties`, items/tuples, enum/const, numeric and string limits, `oneOf`/`anyOf`/`allOf`/`not`, dependencies). `$ref` must be local (`#/...`); remote refs are not resolved, and `contains`, `propertyNames`, `unevaluatedProperties` and `if`/`then`/`else` are not compared. Draft-07 is the focus.
- **Not a byte-for-byte port.** The JSON Schema diff is a clean reimplementation, checked against Confluent's published examples (all 132 agree, verdict and change list), not every edge case of their code. Avro compatibility is checked against 128 cases from Apache Avro's test suite. Registry features such as schema references across subjects, metadata/rule sets and normalization are out of scope.
- **Replay** reads JSON-encoded samples only (plain JSON, or Avro's JSON encoding with wrapped unions), not binary Avro or Confluent wire format. It checks the backward direction (old data, new schema). It finds problems in the samples you keep; it does not generate data.
- **History files come from the PR's own tree.** A PR can edit them. Protect the history folder with CODEOWNERS if that matters to you.
- **The registry still decides.** This catches breaks before merge; the registry's compatibility setting is still what protects production.

## How it compares

| | Needs | Good at | Where this tool differs |
|---|---|---|---|
| [Schema Registry compatibility API](https://docs.confluent.io/platform/current/schema-registry/develop/api.html) (`POST /compatibility/subjects/{subject}/versions`) | A reachable registry and credentials in CI | The authoritative answer for what is registered right now | No registry or network access; compares with git and in-repo history; renders one PR comment with paths and reasons. The API answers `is_compatible` and, with `verbose=true`, raw messages, so you still need glue to post them. |
| [Confluent Maven plugin](https://docs.confluent.io/platform/current/schema-registry/develop/maven-plugin.html) `test-local-compatibility` | JVM and Maven; previous schema files maintained by hand | Offline check with Confluent's own code | Takes the previous version from git automatically; replays samples; Action with PR comment. Confluent's plugin is the more exact implementation. |
| [`buf breaking`](https://buf.build/docs/breaking/) | Protobuf | Protobuf wire and source compatibility, mature | Avro and JSON Schema, with Schema Registry modes. Not a substitute for buf on Protobuf. |
| [vertexclique/degauss](https://github.com/vertexclique/degauss) | Rust | Avro compatibility checks | Adds JSON Schema, git-based PR flow, replay, PR comment. |
| [getsentry/json-schema-diff](https://github.com/getsentry/json-schema-diff) | Rust | Diffing JSON Schemas | Classifies changes with Confluent's compatibility semantics and modes. |
| [asyncapi/diff](https://github.com/asyncapi/diff) | Node | Breaking changes between AsyncAPI documents | Works on raw Avro/JSON Schema files mapped to topics, with Schema Registry modes. |
| [johnmikel/seip](https://github.com/johnmikel/seip) | Node | Making schema-change intent explicit in PRs | Computes compatibility from the schemas themselves. |

## Development

```
make test        # fetches Confluent's examples at a pinned commit, then go test -race ./...
make lint        # go vet + golangci-lint
make golden      # rewrite testdata/golden after an intended output change
make fixture     # build the fixture PR repo in /tmp/schema-gate-fixture
```

Tests: table-driven rule tests per package; 128 reader/writer cases transcribed from Apache Avro's Java test suite (Apache-2.0, see `testdata/avro`); Confluent's JSON Schema diff examples, fetched at a pinned commit by `scripts/fetch-confluent-examples.sh` rather than vendored because they are under the Confluent Community License; golden table/Markdown/JSON reports for the fixture; CLI exit-code tests; and a CI job that runs the Action itself on a fixture PR, twice, and asserts that the PR has exactly one report comment.

All data in this repository is synthetic.

## Name

The working name was `event-contract-guard`. Another public repository already uses that name ([TheEdfog/event-contract-guard](https://github.com/TheEdfog/event-contract-guard), runtime event validation and schema compatibility checks), so this one is named for what it does: a gate on schema evolution.

## License

MIT, see [LICENSE](LICENSE). Avro test cases are derived from Apache Avro (Apache License 2.0).
