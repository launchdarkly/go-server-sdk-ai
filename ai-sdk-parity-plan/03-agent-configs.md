# Task 03 — Agent configs: `AgentConfig`, `AgentConfigs`, agent mode

**Depends on:** 01 (typed configs, tools, `instructions` in datamodel).
**Blocks:** 04 (agent template), 06 (agent graph).

## Objective

Add support for AI Config **agents**: single-agent retrieval (`AgentConfig`),
batch retrieval (`AgentConfigs`) with a per-agent request type, agent-mode
validation, and the `AIAgentConfig` return type (instructions instead of
messages).

## Spec references

- AICONF §1.2.4 — `agentConfig` (params, evaluation steps, mode must be
  `"agent"`, usage event `$ld:ai:usage:agent-config`)
- AICONF §1.2.4.6 — internal evaluation helper accepts optional `graph_key`
  (passed through to the tracker; used by task 06)
- AICONF §1.2.4.7 — returned `AIAgentConfig` carries an `Evaluator` (task 05)
- AICONF §1.2.5 — `agentConfigs` (batch; usage event
  `$ld:ai:usage:agent-configs`, `metric_value` = number of agents; `data` =
  count)
- AICONF §1.3.3 / 1.3.4 — `AIAgentConfig` / `AIAgentConfigDefault`

## Reference implementations

- .NET: `LdAiClient.cs` (`AgentConfig`, `AgentConfigs`),
  `Config/AgentConfigRequest.cs` (`Key`, `DefaultValue`, `Variables`),
  `Config/LdAiAgentConfig.cs`.
- Java: `LDAIClientImpl.java` (`agentConfig`, `agentConfigs`),
  `AIAgentConfigRequest.java` (immutable, `builder(key)` + `defaultValue` +
  `variables`), `AIAgentConfig.java`.

## Current Go state

No agent support. `client.go` has `evaluateConfig` which does completion-style
message interpolation and mode is stored but not validated. The unified `Config`
has `Mode()` but the client does not reject a mode mismatch today.

## Implementation steps

1. **Request type.** Add `AgentConfigRequest` with `Key string`, `DefaultValue
   AIAgentConfigDefault` (optional), `Variables map[string]interface{}`
   (optional). Match .NET's plain-struct shape (Go idiom); a builder is optional.
2. **`AgentConfig` method** on `Client`:
   - Signature: `AgentConfig(key string, context ldcontext.Context,
     defaultValue AIAgentConfigDefault, variables map[string]interface{})
     AIAgentConfig`.
   - Emit usage event `$ld:ai:usage:agent-config` (`data` = `{configKey: key}`,
     `metric_value` = 1) **before** evaluation (mirror `CompletionConfig`).
   - Delegate to an internal helper (see step 4).
3. **`AgentConfigs` method** on `Client`:
   - Signature: `AgentConfigs(requests []AgentConfigRequest, context
     ldcontext.Context) map[string]AIAgentConfig`.
   - For each request, call the **internal** agent-eval helper (NOT the public
     `AgentConfig`, to avoid emitting per-agent usage events).
   - Emit a single aggregate usage event `$ld:ai:usage:agent-configs` with
     `data` = `{agentCount: N}` and `metric_value` = N (AICONF 1.2.5.4).
   - Return a map keyed by agent key. (Java preserves request order via a
     `LinkedHashMap`; a Go `map` has no order — if ordered iteration matters to
     any consumer, document that Go returns an unordered map, matching the
     language's semantics.)
4. **Internal agent-eval helper** `evaluateAgentConfig(key, context,
   defaultValue, variables, graphKey string)`:
   - Reuse the parsing/interpolation core from `evaluateConfig`, but interpolate
     the `instructions` string (not `messages`).
   - Validate `_ldMeta.mode == "agent"`. A missing mode defaults to
     `"completion"`, which is a **mismatch** for agents → log a warning and
     return the default (AICONF 1.2.4.2). Reuse the `returnDefault` pattern.
   - Pass `graphKey` into the tracker factory (task 02) so graph nodes get
     `graphKey` on their events.
   - Attach the `Evaluator` (task 05) built from `judgeConfiguration`.
5. **`AIAgentConfig` type** (from task 01): `Instructions()`, `Tools()`,
   `ModelName()`, `ProviderName()`, model params, `Enabled()`, `CreateTracker()`,
   `Evaluator()`.
6. **Refactor `evaluateConfig`** so completion and agent share the common
   parse/merge-variables/model/provider/tools code; only the messages-vs-
   instructions interpolation and mode check differ.

## Testing

- `AgentConfig`: happy path (mode `agent`, instructions interpolated with
  `variables` + `ldctx`), disabled config, mode mismatch (`completion`/missing →
  default returned + warning logged), non-object variation → default.
- Usage event assertions: `$ld:ai:usage:agent-config` fired once with correct
  data.
- `AgentConfigs`: N requests → N entries; single `$ld:ai:usage:agent-configs`
  event with `metric_value == N` and `agentCount == N`; per-request defaults and
  variables honored; NO per-agent `$ld:ai:usage:agent-config` events emitted.
- `graphKey` passthrough: when the internal helper is called with a graphKey,
  the resulting tracker's events include it (overlaps with task 02/06 tests).

## Acceptance criteria

- `AgentConfig` and `AgentConfigs` implemented per AICONF §1.2.4–1.2.5 with the
  exact event names and metric values above.
- Mode validation returns the default (never nil) on mismatch, with a warning.
- Internal helper supports an optional `graphKey`.
- `go build`, `go test -race`, `golangci-lint run` pass.
