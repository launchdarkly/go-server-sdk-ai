# Task 04 — Template methods (`*ConfigTemplate`)

**Depends on:** 01 (typed configs), 03 (agent path shared code).
**Blocks:** nothing.

## Objective

Add the three "template" retrieval methods that return the raw prompt/instruction
templates with Mustache placeholders **left intact** (no interpolation):
`CompletionConfigTemplate`, `AgentConfigTemplate`, `JudgeConfigTemplate`.

## Spec references

- AICONF §1.2.10 — `completionConfigTemplate` (no `variables` param; skip
  Mustache rendering; store `messages[].content` verbatim; usage event
  `$ld:ai:usage:completion-config-template`)
- AICONF §1.2.11 — `agentConfigTemplate` (verbatim `instructions`; event
  `$ld:ai:usage:agent-config-template`)
- AICONF §1.2.12 — `judgeConfigTemplate` (verbatim messages; event
  `$ld:ai:usage:judge-config-template`)

## Reference implementations

- .NET `Interfaces/ILdAiClient.cs` (`CompletionConfigTemplate`,
  `AgentConfigTemplate`, `JudgeConfigTemplate`) + `LdAiClient.cs`.
- Java `LDAIClientImpl.java` — the same three methods.

## Current Go state

None. `evaluateConfig` always renders Mustache. The interpolation step is the
loop in `evaluateConfig` that calls `interpolateTemplate` on each message; the
agent helper (task 03) does the same for `instructions`.

## Implementation steps

1. Parameterize the internal eval helpers to **skip interpolation**. Cleanest
   approach: add a boolean (e.g. `renderTemplates bool`) or an
   interpolation-function argument to the shared eval core. When rendering is
   skipped, copy `content` / `instructions` verbatim from the variation.
   - Because template methods take **no** `variables` (AICONF 1.2.10.1), pass an
     empty variable map and skip the render call entirely — do not attempt to
     interpolate with an empty map (a stray `{{x}}` would render to empty
     string, which is wrong; it must stay `{{x}}`).
   - Note the judge reserved-placeholder handling in `JudgeConfig`
     (`message_history` / `response_to_evaluate`): for `JudgeConfigTemplate`,
     skip that injection too — the template is returned raw.
2. Add the three public methods on `Client`:
   - `CompletionConfigTemplate(key, context, defaultValue) AICompletionConfig`
   - `AgentConfigTemplate(key, context, defaultValue) AIAgentConfig`
   - `JudgeConfigTemplate(key, context, defaultValue) AIJudgeConfig`
   - No `variables` parameter on any of them.
   - Each emits its `*-template` usage event (same `data`/`metric_value` shape
     as the non-template counterpart) **before** evaluation.
3. Otherwise identical behavior to the non-template method (mode validation,
   default-on-error, tools parsing, tracker factory, evaluator attachment).

## Testing

- For each of the three: assert the returned config's message/instruction
  content still contains the literal `{{placeholder}}` tokens (NOT interpolated).
- Assert the correct `*-template` usage event is emitted and the non-template
  event is NOT emitted.
- Mode-mismatch / disabled / non-object paths return the default, same as the
  non-template methods.

## Acceptance criteria

- Three template methods implemented per AICONF §1.2.10–1.2.12, with no
  `variables` parameter and verbatim template content.
- Correct `$ld:ai:usage:*-config-template` events.
- `go build`, `go test -race`, `golangci-lint run` pass.
