# go-server-sdk-ai

LaunchDarkly Server-side AI SDK for Go.

> **Pre-launch:** no SDK implementation exists here yet. The goal is feature parity with
> [`python-server-sdk-ai`](https://github.com/launchdarkly/python-server-sdk-ai) and
> [`@launchdarkly/server-sdk-ai`](https://github.com/launchdarkly/js-core/tree/main/packages/sdk/server-ai).
> Tracking issue: [AIC-2701](https://launchdarkly.atlassian.net/browse/AIC-2701).

## Overview

Builds on the base [`go-server-sdk`](https://github.com/launchdarkly/go-server-sdk) to manage
AI model configuration (model, provider, prompt messages) via LaunchDarkly AI Configs,
interpolate prompt variables, and report generation metrics (tokens, latency, success/error,
feedback) back to LaunchDarkly.

## Metric event contract

The dashboard reads a fixed set of metric event keys sent via `LDClient.Track`. **These key
strings must match the other SDKs exactly** or the metrics silently fail to register:

- `$ld:ai:duration:total`
- `$ld:ai:tokens:total`, `:input`, `:output`, `:ttf`
- `$ld:ai:generation:success`, `:error`
- `$ld:ai:feedback:user:positive`, `:negative`
- Dynamic judge eval-score keys (one per metric)
- Config-usage events (emitted by both Python and Node): `$ld:ai:config:function:single` /
  `:createChat`, `$ld:ai:agent:function:single` / `:multiple`, `$ld:ai:judge:function:single` /
  `:createJudge`

Metadata differs by event class:
- **Metric events** carry `variationKey`, `configKey`, `version`, `modelName`, `providerName`
  (+ `judgeConfigKey` on judge events). Node also sends `aiSdkName` / `aiSdkVersion`; Python
  does not — Go should follow Node and include them.
- **Config-usage events** instead pass just the config key (or the agent count for `:multiple`)
  as the event data, with value `1` (count for `:multiple`).

## TODO: path to launch

"Launch" = Tier 1 + Tier 2 (a usable SDK whose metrics light up the dashboard).
Tier 3 is post-launch / scope decisions.

### Scaffold
- [ ] `go.mod` (module `github.com/launchdarkly/go-server-sdk-ai`), depend on `go-server-sdk/v7`
- [ ] Package layout (`ldai/` core; providers as separate sub-modules with their own `go.mod`)
- [ ] Repo furniture: usage docs, `CONTRIBUTING`, `LICENSE`, `.golangci.yml`, CI
- [ ] Branch protection on `main` (Terraform follow-up in `launchdarkly/terraform`)

### Tier 1 — usable parity (unblocks dashboard metrics)
- [ ] Data model types: `LDMessage`, model/provider config, default + resolved config variants
- [ ] Client init on top of the base `LDClient`
- [ ] Config retrieval: completion, agent (+ batch), judge — parse `_ldMeta` (enabled / version / mode)
- [ ] Mustache templating with auto-injected `ldctx`, HTML-escaping disabled
- [ ] Tracker: all `Track*` methods using the **exact** event keys above
- [ ] Token-usage normalization (OpenAI + Bedrock shapes) → common `TokenUsage{Total, Input, Output}`
- [ ] Disabled-config behavior (no tracker; chat/judge constructors return nil)

### Tier 2 — higher-level helpers
- [ ] Chat (invoke, message history, accessors)
- [ ] Judge (evaluate, sampling rate, structured-output schema)
- [ ] Provider interface (`InvokeModel`, `InvokeStructuredModel`) + selection factory
- [ ] OpenAI provider (separate sub-module)

### Tier 3 — post-launch / scope decisions
- [ ] LangChain provider (LangChain-Go is less mature — decide whether to ship)
- [ ] Streaming metrics (Go-idiomatic via channels)
- [ ] Agent graphs (currently Python-only)

**Out of scope:** Vercel AI provider (JS-only).

> Implementation notes (concurrency via `context.Context`/errgroup, functional options,
> sub-module providers mirroring the redis/dynamodb/consul split) are in AIC-2701.
