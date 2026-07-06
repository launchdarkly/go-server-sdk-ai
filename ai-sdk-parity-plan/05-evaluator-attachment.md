# Task 05 — `Evaluator` attached to configs (online evals parity)

**Depends on:** 01 (typed configs + `Evaluator` accessor stub).
**Blocks:** nothing (but completes the online-evals surface used by 03/04).

## Objective

Attach an `Evaluator` to every completion and agent config returned by the
client: built from the config's `judgeConfiguration` when present, or a **noop**
`Evaluator` when absent. The `Evaluator` coordinates running all configured
judges for one AI Config invocation in a single call.

The `ldai/judge` package already implements a decoupled `Judge`. This task adds
the `Evaluator` coordination layer and wires it into config evaluation.

## Spec references

- AIEVALS §1.4 — `Evaluator` (instantiation, `noop` factory, `evaluate(input,
  output) -> []JudgeResult`, missing-judge = warn+skip not error, noop = empty
  list, MUST NOT track scores itself)
- AICONF §1.2.3.6 / 1.2.4.7 — returned completion/agent config MUST carry an
  `Evaluator` (noop when no `judgeConfiguration`)
- AIEVALS §1.4.3 — SDK client builds + attaches the `Evaluator`; user code does
  not construct it
- AIEVALS §1.3.1 — `JudgeResult` shape (`judgeConfigKey`, `success`,
  `errorMessage`, `sampled`, `metricKey`, `score`, `reasoning`)

## Reference implementations

- Java `Evaluator.java`, `Judge.java` — canonical structure.
- .NET has judge/eval types under `Config/`+`Tracking/JudgeResult.cs`.

## Current Go state

- `ldai/judge/judge.go`: `Judge` with `Evaluate(input, output, samplingRate)
  (*JudgeResponse, error)`, `EvaluateMessages`, structured parsing. It takes a
  `Provider` (model-invocation) interface — keep this decoupled design.
- `ldai/datamodel`: `JudgeResponse`, `EvalScore`, `JudgeConfiguration`, `Judge`
  (key + samplingRate).
- No `Evaluator` type. Configs do not expose an evaluator. `Tracker` has
  `TrackJudgeResponse` (used to track scores — this is the *caller's* job per
  AIEVALS 1.1.2.8 / 1.4.2.5, so keep it on the tracker, not the Evaluator).

## Design notes / decisions

- **`JudgeResult` vs `JudgeResponse`:** the Go code currently uses
  `datamodel.JudgeResponse`. The spec's `JudgeResult` (AIEVALS 1.3.1) adds a
  `sampled` boolean and `metricKey`. Decide whether to (a) extend
  `JudgeResponse` with `Sampled`/`MetricKey` to match the spec, or (b) add a new
  `JudgeResult`. **Recommend (a)** to avoid a parallel type; the current
  `Judge.Evaluate` returns `nil` on a sampling skip — change it to return a
  result with `Sampled=false` instead (AIEVALS 1.1.2.3), so the Evaluator can
  decide whether to include it. Confirm with maintainers since it changes the
  judge package's public return contract.
- **How judges get their model provider:** `judge.New` requires a `Provider`.
  The client cannot fabricate a provider for the user's LLM. Check how .NET/Java
  build the judges attached to a config: they resolve a provider/runner by the
  judge config's provider name. Since Go deliberately skips the runner layer,
  the Evaluator built by the client will only be able to actually *run* judges
  if the application supplies a provider factory. **Options:**
  1. Attach a **noop-capable** Evaluator that holds the `JudgeConfiguration` and
     the judge configs, but requires the caller to supply a provider (e.g.
     `config.Evaluator().WithProvider(p).Evaluate(...)`), OR
  2. Add an optional provider-factory to `NewClient` (client option) so the
     client can build fully-runnable judges.
  **Recommend surfacing this to maintainers**; mirror whatever .NET/Java expose.
  At minimum, satisfy AICONF 1.2.3.6/1.2.4.7 by always attaching a **non-nil**
  Evaluator (noop when no `judgeConfiguration`) so callers never nil-check.

## Implementation steps

1. Add an `Evaluator` type (new file `ldai/evaluator.go`):
   - Fields: `judges map[string]*judge.Judge`, `judgeConfiguration
     *datamodel.JudgeConfiguration`.
   - `NoopEvaluator()` factory → empty judges, empty configuration.
   - `Evaluate(input, output string) ([]datamodel.JudgeResult, error)`:
     iterate `judgeConfiguration.Judges`; for each, look up the judge by key;
     if missing → log warning + skip (not an error, AIEVALS 1.4.2.3.1); else
     call `judge.Evaluate(input, output, samplingRate)` and collect results.
     Noop → return empty slice immediately (AIEVALS 1.4.2.4).
   - MUST NOT call any tracker method (AIEVALS 1.4.2.5).
2. Build + attach the Evaluator during `evaluateConfig` / `evaluateAgentConfig`
   (tasks 01/03): if `judgeConfiguration` present, build a `Judge` per key
   (fetching each judge config via the internal judge-config helper) and build
   an `Evaluator`; else attach `NoopEvaluator()`.
   - Fetching child judge configs must NOT emit `$ld:ai:usage:judge-config`
     (use the internal helper, per the createJudge double-count guidance in
     AIEVALS 1.2.1.6 which we generalize here).
3. Expose `Evaluator()` on `AICompletionConfig` and `AIAgentConfig` (fills in
   the task-01 stub). Never returns nil.
4. If the provider-injection decision (design note) lands on a client option,
   add it to `NewClient` as a functional option and thread it through.

## Testing

- Noop evaluator (no `judgeConfiguration`) → `Evaluate` returns empty slice, no
  warnings, no tracker calls.
- Evaluator with two judge keys, one missing from the judges map → warns +
  skips the missing one; returns one result.
- Sampling: with `samplingRate=0`, result has `Sampled=false`; with `1.0`,
  judge is invoked (`Sampled=true`). (Use a fake `Provider`.)
- Config methods attach a non-nil evaluator in all paths (present + absent
  `judgeConfiguration`, and on the default/error path).

## Acceptance criteria

- Every completion/agent config carries a non-nil `Evaluator` (noop when no
  judges), per AICONF 1.2.3.6/1.2.4.7 & AIEVALS 1.4.3.
- `Evaluator.Evaluate` coordinates judges, skips missing ones with a warning,
  never tracks scores itself.
- The `JudgeResult`/`sampled` decision is implemented and approved.
- `go build`, `go test -race`, `golangci-lint run` pass.
