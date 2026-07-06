# Task 07 — Graph tracking: `AIGraphTracker`, `AIGraphMetricSummary`, `CreateGraphTracker`

**Depends on:** 02 (tracker patterns), 06 (`AgentGraphDefinition.CreateTracker`).
**Blocks:** nothing (final capability).

## Objective

Implement the graph-level tracker (`AIGraphTracker`) with graph-level and
edge-level track methods, a metric summary, resumption tokens, and the client
`CreateGraphTracker` reconstruction method.

## Spec references

- AIGRAPHTRACK §1.1 — instantiation (`ldclient`, `variationKey?`, `graphKey`
  (required), `version`, `context`, `runId?` → generate UUIDv4 if absent);
  `getTrackData` (camelCase `runId`, `variationKey?`, `graphKey`, `version`);
  `getSummary` → `AIGraphMetricSummary`; at-most-once for graph-level metrics
  (edge-level methods are multi-fire); `resumptionToken` +
  `from_resumption_token`.
- AIGRAPHTRACK §1.1.3.2 — `AIGraphMetricSummary`: `success?`, `duration?`
  (`durationMs`), `tokens?` (`TokenUsage`), `path?` (`[]string`).
- AIGRAPHTRACK §1.1.5.1 — resumption token = URL-safe Base64 (no padding) of
  canonical JSON with keys in this exact order: `runId`, `graphKey`,
  `variationKey` (omit if absent), `version`.
- AIGRAPHTRACK §1.2 — graph-level methods & event names:
  - `TrackInvocationSuccess` → `$ld:ai:graph:invocation_success` (mv=1)
  - `TrackInvocationFailure` → `$ld:ai:graph:invocation_failure` (mv=1)
  - `TrackDuration(ms)` → `$ld:ai:graph:duration:total` (mv=ms)
  - `TrackTotalTokens(TokenUsage)` → `$ld:ai:graph:total_tokens` (mv=total)
  - `TrackPath([]string)` → `$ld:ai:graph:path` (mv=1; `data` adds `path`)
- AIGRAPHTRACK §1.3 — edge-level methods (multi-fire, NOT at-most-once):
  - `TrackRedirect(sourceKey, redirectedTarget)` → `$ld:ai:graph:redirect`
  - `TrackHandoffSuccess(sourceKey, targetKey)` → `$ld:ai:graph:handoff_success`
  - `TrackHandoffFailure(sourceKey, targetKey)` → `$ld:ai:graph:handoff_failure`
  - each adds `sourceKey`/`targetKey` (or `redirectedTarget`) to `data`, mv=1
- AICONF §1.2.9 — `AIClient.CreateGraphTracker(token, context)` delegates to
  `AIGraphTracker.from_resumption_token`.

## Reference implementations

- .NET `Graph/AiGraphTracker.cs`, `Tracking/AiGraphMetricSummary.cs`;
  `LdAiClient.CreateGraphTracker`.
- Java `AIGraphTracker.java`, `AIGraphMetricSummary.java`;
  `LDAIClientImpl.createGraphTracker`.

## Current Go state

None. `Tracker` (config-level) exists and is a good structural template
(at-most-once via `ldcommon.Option[...]`, `TrackMetric` event sink, resumption
token via `resumptionPayload` + `base64.RawURLEncoding`). Reuse its patterns.

## Implementation steps

1. **`AIGraphTracker`** (`ldai/graph_tracker.go`):
   - Fields: `events EventSink`, `runID`, `graphKey`, `variationKey`, `version`,
     `context`, `logger`, `stopwatch` (optional), + `Option`-typed summary
     fields: `success`, `duration`, `tokens`, `path`.
   - Build `trackData` once (camelCase `runId`, `graphKey`, `variationKey` when
     set, `version`; plus `aiSdkName`/`aiSdkVersion` to match the config tracker).
   - Constructor generates a UUIDv4 `runId` when none supplied
     (AIGRAPHTRACK 1.1.1.1) — reuse `newRunID()`.
   - `GetSummary() AIGraphMetricSummary`.
   - Graph-level methods enforce at-most-once (check the `Option`, warn + drop if
     already set — AIGRAPHTRACK 1.1.4.1). Edge-level methods do **not**.
   - `TrackPath` adds `path` to a copy of `trackData`. Edge methods add
     `sourceKey`/`targetKey`/`redirectedTarget` to a copy of `trackData`
     (mirror the copy pattern in `Tracker.TrackJudgeResponse`).
2. **`AIGraphMetricSummary`** type: `Success ldcommon.Option[bool]`,
   `DurationMs ldcommon.Option[time.Duration]` (or ms int), `Tokens
   ldcommon.Option[TokenUsage]`, `Path ldcommon.Option[[]string]`.
3. **Resumption token**: a `graphResumptionPayload` with fields in the exact
   order `runId`, `graphKey`, `variationKey` (omitempty), `version`. Encode with
   `base64.RawURLEncoding`. Add `GraphTrackerFromResumptionToken(token, sdk,
   context)` mirroring `TrackerFromResumptionToken`. **Important:** Go's
   `encoding/json` marshals struct fields in declaration order, so declare the
   struct fields in the required order; do not rely on map ordering. Add a test
   asserting the exact byte output for a known payload.
4. **`AgentGraphDefinition.CreateTracker()`** (task 06 stub) returns a fresh
   `AIGraphTracker` seeded with the graph's `graphKey`, `variationKey`,
   `version`, and the context used to fetch the graph.
5. **`Client.CreateGraphTracker(token, context)`** delegates to
   `GraphTrackerFromResumptionToken` (AICONF 1.2.9).
6. Document the security note (token embeds variationKey/version — keep
   server-side) in the method doc comments (AIGRAPHTRACK 1.1.5.2 security note).

## Testing

- Event-name/metric-value assertions for all graph- and edge-level methods,
  against a mock event sink.
- At-most-once: second `TrackInvocationSuccess`/`TrackDuration`/etc. is dropped
  with a warning; edge methods can fire repeatedly.
- `GetSummary` reflects tracked values.
- Resumption token: exact-bytes round-trip test for a known
  `{runId,graphKey,variationKey,version}` (and the variant with `variationKey`
  omitted); `CreateGraphTracker` reconstructs a tracker with the same `runId`.
- `AgentGraphDefinition.CreateTracker()` mints a new `runId` per call.

## Acceptance criteria

- `AIGraphTracker` implements every AIGRAPHTRACK method with the exact event
  names, metric values, camelCase data keys, and at-most-once vs. multi-fire
  semantics.
- Canonical, order-stable resumption token; `CreateGraphTracker` reconstructs
  the run identity.
- `go build`, `go test -race`, `golangci-lint run` pass.
