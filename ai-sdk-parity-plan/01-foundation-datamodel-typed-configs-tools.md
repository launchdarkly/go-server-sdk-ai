# Task 01 — Foundation: datamodel, typed configs & defaults, tools

**Depends on:** nothing. **Blocks:** 03, 04, 05, 06.

## Objective

Establish the data-model and type foundation the rest of the parity work builds
on:

1. Parse and expose the root-level `tools` map (`ToolConfig`) on completion and
   agent configs.
2. Introduce typed config + default types (`AICompletionConfig`,
   `AIAgentConfig`, `AIJudgeConfig` and their `*Default` builders) to match the
   spec (AICONF §1.3) and .NET/Java, **without gratuitously breaking** the
   existing `Config` API.

## Spec references

- AICONF §1.1 (JSON protocol, `tools`, `provider` with additional properties)
- AICONF §1.3.1–1.3.6 (typed configs + defaults)
- AICONF §1.3.3.1 / 1.3.3.1.1 (root `tools` map is a sibling of `model`; SDK
  MUST NOT modify `model.parameters.tools[]`)

## Reference implementations

- .NET `pkgs/sdk/server-ai/src/Config/`: `LdAiCompletionConfig.cs`,
  `LdAiAgentConfig.cs`, `LdAiJudgeConfig.cs`, `LdAiConfig.cs`,
  `LdAiConfigTypes.cs` (contains `Tool` record with `CustomParameters`),
  `LdAiCompletionConfigDefault.cs`, `LdAiAgentConfigDefault.cs`,
  `LdAiJudgeConfigDefault.cs`, `ConfigFactory.cs`.
- Java `.../ai/`: `AICompletionConfig.java`, `AIAgentConfig.java`,
  `AIJudgeConfig.java`, `AIConfig.java` (shared base), the `*Default.java`
  files, and `datamodel/LDAIConfigTypes.java`.

## Current Go state

`ldai/config.go` exposes a single `Config` struct + `ConfigBuilder`; `Disabled()`
returns a disabled `Config`. `ldai/datamodel/datamodel.go` has `Config`, `Meta`,
`Model`, `Provider`, `Message`, `JudgeConfiguration`, `Judge`, no `tools`.
`ldai/client.go` builds a `Config` via the builder in `evaluateConfig`.

## Design decision (make this call first, confirm with maintainers)

Two viable options; **recommend Option B**:

- **Option A — keep the unified `Config`.** Add `Tools()`, `Instructions()`,
  and typed default constructors as thin wrappers, but keep one struct.
  Lowest churn; diverges most from spec/other SDKs and makes agent/judge/graph
  code muddier.
- **Option B — introduce typed configs (recommended).** Add `AICompletionConfig`,
  `AIAgentConfig`, `AIJudgeConfig` that embed/compose a shared internal base
  (holding key, enabled, model, provider, tools, tracker factory, evaluator).
  Keep the existing `Config` type and `CompletionConfig` method working as a
  **deprecated alias** for `AICompletionConfig` to avoid a hard breaking change,
  or type-alias `Config = AICompletionConfig` if signatures allow. Mirror the
  .NET/Java split. This makes tasks 03–07 clean.

Whichever is chosen, the returned config types MUST retain:
- `CreateTracker()` (AICONF 1.2.7) — via the existing `trackerFactory` hook.
- An `Evaluator` accessor (task 05 fills this in; add the field + a nil-safe
  accessor now, defaulting to a noop once task 05 lands).

Document the decision in the PR description and in `00-OVERVIEW.md` if it changes.

## Implementation steps

### 1. Datamodel: tools + provider passthrough

In `ldai/datamodel/datamodel.go`:

- Add a `Tool` (wire) struct:
  ```go
  type Tool struct {
      Name             string                    `json:"name"`
      Description      string                    `json:"description,omitempty"`
      Type             string                    `json:"type,omitempty"`
      Parameters       map[string]ldvalue.Value  `json:"parameters,omitempty"`
      CustomParameters map[string]ldvalue.Value  `json:"customParameters,omitempty"`
  }
  ```
- Add `Tools map[string]Tool `json:"tools,omitempty"`` to the `Config` wire
  struct (sibling of `Model`, `Provider`). Add `instructions` too so this task
  and task 03 share the datamodel:
  `Instructions string `json:"instructions,omitempty"``.
- Do **not** touch `model.parameters.tools[]` — that array stays inside
  `Model.Parameters` untouched (AICONF 1.3.3.1.1).
- `provider` in the wire format may carry extra properties; today `Provider`
  only has `Name`. Keep `Name` required; additional provider properties are not
  currently surfaced by any reference SDK's public API, so leaving them
  unparsed is acceptable — note it.

### 2. Public `ToolConfig` type + accessors

- Add a public `ToolConfig` type (in `ldai`, e.g. `tools.go`) exposing
  `Name()`, `Description()`, `Type()`, `Parameters()`, `CustomParameters()`
  with defensive copies (mirror the `slices.Clone`/`maps.Clone` pattern in
  `config.go`).
- Expose `Tools() map[string]ToolConfig` on completion and agent config types
  (AICONF 1.3.3.1). Return a defensive copy.

### 3. Typed configs & defaults (Option B)

- Create `AICompletionConfig` (messages, model, provider, tools, enabled, key,
  tracker factory, evaluator), `AIAgentConfig` (instructions instead of
  messages; + tools), `AIJudgeConfig` (messages + `EvaluationMetricKey`).
- Create `AICompletionConfigDefault`, `AIAgentConfigDefault`,
  `AIJudgeConfigDefault`:
  - Each has a `Disabled()` static/helper (AICONF 1.3.2.1/1.3.4.1/1.3.6.1).
  - Each `enabled` field **defaults to `true`** when the default object is
    explicitly constructed (AICONF 1.3.2/1.3.4/1.3.6) — but `Disabled()`
    yields `enabled=false`. Match .NET/Java exactly here.
  - Each MUST provide the `AsLdValue()`/marshal mechanism used as the default
    value for `JSONVariation` (AICONF 1.3.2.2 etc.) — the current
    `Config.AsLdValue()` already does this; replicate per type.
- Keep `Config`/`ConfigBuilder`/`Disabled()` as deprecated aliases so existing
  callers and `client.go` keep compiling. Update `evaluateConfig` in a
  follow-up within this task to build the typed completion config.

### 4. Refactor `client.go` builder path

- Update `evaluateConfig` to parse `tools` and (for task 03) `instructions`,
  and to build the appropriate typed config. To keep this task self-contained,
  it may still only produce the completion path; task 03 generalizes it.

## Testing

- Datamodel round-trip tests: unmarshal the exact AICONF §1.1 example JSON
  (completion + judge; include the `tools` map and a `model.parameters.tools[]`
  array) and assert both are parsed and that `model.parameters.tools[]` is left
  untouched.
- `ToolConfig` accessor tests incl. defensive-copy behavior.
- Typed-default tests: `Disabled()` → `enabled=false`; explicitly-built default
  → `enabled=true`; `AsLdValue()` marshals to the expected JSON.
- Ensure existing `client_test.go` / `config_test.go`-style tests still pass.

## Acceptance criteria

- `tools` map parsed and exposed on completion + agent configs;
  `model.parameters.tools[]` untouched.
- Typed configs + typed defaults exist with `Disabled()` helpers and marshal
  correctly as `JSONVariation` defaults.
- A nil-safe `Evaluator` accessor exists on completion/agent configs (wired in
  task 05).
- `go build ./...`, `go test -race ./...`, `golangci-lint run` all pass.
- Backward compatibility preserved (or breaking changes explicitly called out
  and approved).
