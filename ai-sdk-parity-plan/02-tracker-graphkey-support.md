# Task 02 — `AIConfigTracker` graphKey support (+ trackMetricsOf assessment)

**Depends on:** nothing. **Blocks:** 06, 07 (agent-graph node trackers must carry `graphKey`).

## Objective

Allow an `AIConfigTracker` (the Go `Tracker`) to be created with a `graphKey`
so that every event it emits includes `graphKey` in its `trackData`. This is the
prerequisite for agent-graph node trackers (task 06) to attribute node-level
events to their containing graph.

Also assess whether `trackMetricsOf` (AITRACK §1.1.15/1.1.16 area) is needed for
Go parity; the reference SDKs implement it but Go already has the equivalent
`TrackRequest`. See "trackMetricsOf" below.

## Spec references

- AITRACK §1.1.1 — `graph_key` (optional) constructor param
- AITRACK §1.1.2 — `getTrackData` includes optional `graphKey` (camelCase)
- AITRACK §1.1.2.2 — MUST include `graphKey` when available; set once at
  instantiation and included in every emitted event

## Reference implementations

- .NET `LdAiConfigTracker.cs` + `Tracking/` — the tracker's track-data builder
  includes `graphKey` when set.
- Java `internal/LDAIConfigTrackerImpl.java`, `LDAIConfigTracker.java`.

## Current Go state

`ldai/tracker.go`: `newTracker(events, runID, key, variationKey, version, ctx,
config, loggers)` builds `trackData` with `runId`, `configKey`, `version`,
`providerName`, `modelName`, `aiSdkName`, `aiSdkVersion`, and `variationKey`
(when non-empty). There is **no** `graphKey`.

## Implementation steps

1. Add an optional `graphKey string` to the tracker.
   - Extend `newTracker` / `newTrackerWithStopwatch` with a `graphKey` param
     (or, to minimize churn, add a functional-option or a
     `newTrackerWithGraphKey` variant). Prefer a single extended signature since
     all callers are internal.
   - When `graphKey != ""`, add `.Set("graphKey", ldvalue.String(graphKey))` to
     the `trackData` builder in `newTrackerWithStopwatch`.
2. Thread the value through the config's `trackerFactory` so that when a config
   is fetched "in the context of a graph node" (task 06 / AICONF 1.2.4.6) the
   factory closes over the graph key. In `client.go`, the internal agent-config
   evaluation helper (task 03) should accept an optional `graphKey` and pass it
   into `newTracker`.
3. Keep `graphKey` out of the **resumption token** for config trackers — the
   `resumptionPayload` (runId/configKey/variationKey/version) is unchanged.
   (Graph-level tokens are a separate structure — task 07.)

### trackMetricsOf

- Go's `Tracker.TrackRequest(func(*Config) (ProviderResponse, error))` already
  implements the "run an operation, auto-measure duration, track
  success/error + tokens + TTFT" behavior of AITRACK `trackMetricsOf`.
- **Recommendation:** do **not** add a second method unless task 05
  (`Evaluator`) needs the exact `trackMetricsOf(operation, extractor)` shape.
  If task 05 needs it, add a thin `TrackMetricsOf` wrapper around the existing
  logic rather than duplicating. Decide in task 05; leave a note here.

## Testing

- Unit test: tracker created with a `graphKey` includes `"graphKey"` in the
  `data` payload of every `TrackMetric` call (assert against the mock event
  sink used in `tracker_test.go`).
- Unit test: tracker created **without** a `graphKey` does **not** include the
  key (omitted, not empty string).
- Existing tracker tests must still pass unchanged.

## Acceptance criteria

- Trackers can be constructed with a `graphKey`; when set it appears in every
  event's `trackData` under `graphKey`; when unset it is omitted.
- Resumption-token shape for config trackers is unchanged.
- `go build`, `go test -race`, `golangci-lint run` pass.
