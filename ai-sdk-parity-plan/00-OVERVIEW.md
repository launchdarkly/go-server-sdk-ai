# go-server-sdk-ai: AI SDK Parity Plan — Overview

This directory contains a set of hand-off-ready task plans for bringing the Go
AI SDK (`github.com/launchdarkly/go-server-sdk-ai`, package `ldai`) up to
feature parity with the LaunchDarkly AI SDK specification and the recently
"brought to parity" .NET (`launchdarkly/dotnet-core`) and Java
(`launchdarkly/java-core`) implementations.

Each numbered file is an independent, self-contained task designed to be picked
up by another agent. Read this overview first, then the specific task file.

## Source of truth

- **Spec (authoritative):** `launchdarkly/sdk-specs`, path
  `specs/AISDK-ai-sdk/`. Sub-specs:
  - `AICONF-ai-config` — AI Config / Agent Config / Judge Config, client methods, types
  - `AITRACK-ai-tracking-&-performance` — `AIConfigTracker`
  - `AIEVALS-ai-online-evals` — `Judge`, `Evaluator`
  - `AIGRAPH-ai-graph` — agent graph model, traversal, `agentGraph` client method
  - `AIGRAPHTRACK-ai-graph-tracking` — `AIGraphTracker`
  - `AIRUNNER-ai-runner` (+ `MODEL`, `AGENT`, `GRAPH`) — **out of scope**, see below
- **Reference implementations (parity target):**
  - .NET: `launchdarkly/dotnet-core` → `pkgs/sdk/server-ai/src`
  - Java: `launchdarkly/java-core` → `lib/sdk/server-ai/src/main/java/com/launchdarkly/sdk/server/ai`
  - Also useful: Python `launchdarkly/python-server-sdk-ai`, JS
    `launchdarkly/js-core` → `packages/sdk/server-ai`

When the Go implementation must make a judgment call, prefer whatever .NET and
Java did (they are the most recent parity pass), and cite the spec requirement
number in code comments / PR descriptions.

## In scope vs. out of scope

**In scope** (this plan): everything the .NET/Java `server-ai` packages ship
today — completion configs, agent configs (single + batch), judge configs,
`*Template` variants, root-level `tools`, the `Evaluator` attached to configs,
agent graphs + graph traversal, and graph tracking.

**Out of scope** (explicitly skipped — experimental): the **AI Runner Layer**
(`AIRUNNER` spec and its `MODEL`/`AGENT`/`GRAPH` children) — i.e. the
`ManagedModel`, `ManagedAgent`, `ManagedAgentGraph` types and the client
`createModel` / `createAgent` / `createJudge` factory methods. Neither .NET nor
Java ship these managed/`create*` factories, and the user has designated them
experimental. Do **not** implement them.

> Note on the `Judge`/`Evaluator`: the `AIEVALS` spec technically layers `Judge`
> on top of the experimental `Runner`. The reference SDKs (and the current Go
> code) implement a **decoupled** `Judge` that takes a provider/model-invocation
> interface directly rather than the managed `Runner`. Keep that decoupled
> approach — see task `05`.

## Current state of the Go SDK (as of this plan)

Already implemented in `ldai/`:

- `Client` with `CompletionConfig`, `JudgeConfig`, and `CreateTracker` (from a
  resumption token). SDK-info tracking (`$ld:ai:sdk:info`) fires at construction.
- A **single unified** `Config` type (`config.go`) with a `ConfigBuilder`,
  `Mode()`, messages, model params/custom, provider, `evaluationMetricKey`,
  `judgeConfiguration`. `Disabled()` helper.
- `Tracker` (`tracker.go`): duration, feedback, success/error, tokens, TTFT,
  `TrackRequest`, `TrackJudgeResponse`, resumption tokens. At-most-once
  semantics for graph-scoped metrics.
- `ldai/judge` package: a decoupled `Judge` + structured-output parsing + schema.
- Mustache interpolation with HTML-escaping disabled (`client.go`).
- `ldai/datamodel`: wire types for config, meta, model, provider, message,
  judge configuration, judge response.

Base SDK: `go-server-sdk/v7`, common `go-sdk-common/v4`. Go 1.24+. Mustache lib:
`github.com/alexkappa/mustache`. UUID: `github.com/google/uuid`.

## Gap analysis (what's missing vs. .NET/Java parity)

| Capability | Spec | .NET/Java | Go today | Task |
|---|---|---|---|---|
| `completionConfig` | AICONF 1.2.3 | ✅ | ✅ | — |
| `judgeConfig` | AICONF 1.2.6 | ✅ | ✅ | — |
| `CreateTracker` (resumption) | AICONF 1.2.8 | ✅ | ✅ | — |
| Typed configs (`AICompletionConfig`/`AIAgentConfig`/`AIJudgeConfig`) + typed defaults | AICONF 1.3 | ✅ | ❌ (unified `Config`) | 01 |
| Root-level `tools` map + `ToolConfig` | AICONF 1.1 / 1.3.3.1 | ✅ | ❌ | 01 |
| `graphKey` on `AIConfigTracker` | AITRACK 1.1.1/1.1.2.2 | ✅ | ❌ | 02 |
| `agentConfig` (single) | AICONF 1.2.4 | ✅ | ❌ | 03 |
| `agentConfigs` (batch) + request type | AICONF 1.2.5 | ✅ | ❌ | 03 |
| `completionConfigTemplate` / `agentConfigTemplate` / `judgeConfigTemplate` | AICONF 1.2.10–12 | ✅ | ❌ | 04 |
| `Evaluator` attached to configs (noop when none) | AIEVALS 1.4 / AICONF 1.2.3.6 | ✅ | ❌ | 05 |
| Agent graph model + `AgentGraphNode` + `AgentGraphDefinition` + traversal | AIGRAPH | ✅ | ❌ | 06 |
| `agentGraph` client method + validation | AIGRAPH 1.5 | ✅ | ❌ | 06 |
| `AIGraphTracker` + `AIGraphMetricSummary` + `createGraphTracker` | AIGRAPHTRACK | ✅ | ❌ | 07 |
| Docs / examples / test parity | — | ✅ | partial | 08 |

## Task dependency graph

```
01 (foundation: typed configs, defaults, tools, datamodel)
├── 03 (agent configs)          depends on 01
│   └── 06 (agent graph)        depends on 01, 02, 03
│       └── 07 (graph tracking) depends on 02, 06
├── 04 (template methods)       depends on 01, 03
└── 05 (evaluator attachment)   depends on 01
02 (tracker graphKey support)   no deps; needed by 06/07
08 (docs, examples, tests)      depends on all
```

Recommended execution order: **01 → 02 → 03 → 04 → 05 → 06 → 07 → 08**. Tasks
02, 04, and 05 can be parallelized once 01 lands. 03 must precede 06.

## Cross-cutting conventions (apply to every task)

- **Event key prefix:** all AI events use `$ld:ai:` (usage metrics use
  `$ld:ai:usage:`). See AISDK 1.2. Every literal event name is given in the
  relevant task; do not invent new ones.
- **Tracking metadata (`trackData`)** uses **camelCase** keys (`runId`,
  `configKey`, `variationKey`, `version`, `modelName`, `providerName`,
  `graphKey`). Omit optional keys when empty.
- **Evaluation delegation:** client methods MUST call `JSONVariation`
  (never `variation_detail`). See AICONF 1.2.3.3.
- **Defaults never null:** a config method must always return a usable config
  (disabled default on any error / mode mismatch), never nil.
- **`ldctx` variable:** the interpolation variable map is seeded from the
  caller's `variables`, then an `ldctx` entry (the context as attributes) is
  added **last** so it always wins. Reserved; warn + skip if the caller passes
  `ldctx`. This is already implemented in `client.go` (`getAllAttributes`).
- **Errors package:** use `github.com/pkg/errors` if depguard complains; the
  current code uses stdlib `fmt.Errorf` which is fine for this repo (verify with
  `golangci-lint run` — this repo's `.golangci.yml`, not gonfalon's rules).
- **Concurrency:** match existing doc comments — `Client`/`Tracker` methods are
  documented as not safe for concurrent use unless stated.

## Testing & CI (see CONTRIBUTING.md)

- Build: `go build ./...`
- Unit tests (as CI runs them): `go test -race -coverprofile=coverage.out ./...`
- Lint: `golangci-lint run` (golangci-lint v2 config; CI uses action v8+).
- CI matrix: Go `1.24` and `stable`.
- Every task must add table-driven unit tests using `stretchr/testify`,
  mirroring the coverage style of existing `*_test.go` files
  (`client_test.go`, `tracker_test.go`, `judge/judge_test.go`). Aim to cover
  each new spec requirement with at least one assertion, and reference the
  requirement number in the test name/comment.
- Provider sub-modules (e.g. OpenAI) live in their own `go.mod` per
  CONTRIBUTING "Code organization"; nothing in this plan requires touching them.

## A note on the unified-`Config` design decision (read before task 01)

The Go SDK currently exposes one `Config` type for all modes; the spec and
.NET/Java expose three distinct types. Task 01 covers the recommended approach
(introduce typed configs while preserving the existing API where practical).
Because this is the one place where Go deliberately diverges, the task 01 owner
should surface the chosen approach to the maintainers before writing large
amounts of code. Everything downstream (03–07) assumes the task-01 decision is
made.
