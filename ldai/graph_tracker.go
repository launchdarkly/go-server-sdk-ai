package ldai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	ldcommon "github.com/launchdarkly/go-sdk-common/v4"
	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

const (
	graphInvocationSuccess = "$ld:ai:graph:invocation_success"
	graphInvocationFailure = "$ld:ai:graph:invocation_failure"
	graphDurationTotal     = "$ld:ai:graph:duration:total"
	//nolint:gosec
	graphTotalTokens    = "$ld:ai:graph:total_tokens"
	graphPath           = "$ld:ai:graph:path"
	graphRedirect       = "$ld:ai:graph:redirect"
	graphHandoffSuccess = "$ld:ai:graph:handoff_success"
	graphHandoffFailure = "$ld:ai:graph:handoff_failure"
)

// graphResumptionPayload is the JSON structure encoded into a graph resumption token.
// Field order is intentional: runId, graphKey, optional variationKey, version.
type graphResumptionPayload struct {
	RunID        string `json:"runId"`
	GraphKey     string `json:"graphKey"`
	VariationKey string `json:"variationKey,omitempty"`
	Version      int    `json:"version"`
}

// GraphMetricSummary is a snapshot of graph-level metrics recorded by a GraphTracker.
type GraphMetricSummary struct {
	// Success is true after TrackInvocationSuccess, false after TrackInvocationFailure,
	// or unset if neither has been recorded.
	Success ldcommon.Option[bool]
	// DurationMs is the tracked graph-level duration in milliseconds, if recorded.
	DurationMs ldcommon.Option[float64]
	// Tokens is the tracked token usage, if recorded.
	Tokens ldcommon.Option[TokenUsage]
	// Path is the ordered list of node keys visited, or nil if not recorded.
	Path []string
	// ResumptionToken can reconstruct this graph tracker in another process.
	ResumptionToken string
}

// GraphTracker records graph-level metrics for a single agent graph invocation.
// Unless otherwise noted, GraphTracker methods are not safe for concurrent use.
//
// Graph-level methods (invocation, duration, tokens, path) are at-most-once.
// Edge-level methods (redirect, handoff) are multi-fire.
type GraphTracker struct {
	runID        string
	graphKey     string
	variationKey string
	version      int
	context      ldcontext.Context
	events       EventSink
	trackData    ldvalue.Value
	logger       interfaces.LDLoggers

	success  ldcommon.Option[bool]
	duration ldcommon.Option[float64]
	tokens   ldcommon.Option[TokenUsage]
	path     ldcommon.Option[[]string]
}

func newGraphTracker(
	events EventSink,
	runID string,
	graphKey string,
	variationKey string,
	version int,
	ctx ldcontext.Context,
	loggers interfaces.LDLoggers,
) *GraphTracker {
	builder := ldvalue.ObjectBuild().
		Set("runId", ldvalue.String(runID)).
		Set("graphKey", ldvalue.String(graphKey)).
		Set("version", ldvalue.Int(version))
	if variationKey != "" {
		builder.Set("variationKey", ldvalue.String(variationKey))
	}

	return &GraphTracker{
		runID:        runID,
		graphKey:     graphKey,
		variationKey: variationKey,
		version:      version,
		context:      ctx,
		events:       events,
		trackData:    builder.Build(),
		logger:       loggers,
	}
}

func (t *GraphTracker) logWarning(msg string) {
	prefix := "AI Graph tracker for '" + t.graphKey + "': "
	t.logger.Warnf(prefix+"%s %s", msg, t.trackData.JSONString())
}

func (t *GraphTracker) logDebug(format string, args ...interface{}) {
	prefix := "AI Graph tracker for '" + t.graphKey + "': "
	t.logger.Debugf(prefix+format, args...)
}

// ResumptionToken returns a URL-safe Base64-encoded token for reconstructing this GraphTracker.
func (t *GraphTracker) ResumptionToken() string {
	payload := graphResumptionPayload{
		RunID:        t.runID,
		GraphKey:     t.graphKey,
		VariationKey: t.variationKey,
		Version:      t.version,
	}
	jsonBytes, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(jsonBytes)
}

// TrackerGraphFromResumptionToken reconstructs a GraphTracker from a token produced by
// GraphTracker.ResumptionToken, reusing the original runId.
func TrackerGraphFromResumptionToken(token string, sdk ServerSDK, context ldcontext.Context) (*GraphTracker, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid graph resumption token: %w", err)
	}
	var payload graphResumptionPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("invalid graph resumption token: %w", err)
	}
	if payload.RunID == "" || payload.GraphKey == "" {
		return nil, fmt.Errorf("invalid graph resumption token: missing required fields (runId, graphKey)")
	}
	return newGraphTracker(
		sdk,
		payload.RunID,
		payload.GraphKey,
		payload.VariationKey,
		payload.Version,
		context,
		sdk.Loggers(),
	), nil
}

// TrackInvocationSuccess records that the graph invocation succeeded.
//
// At-most-once and mutually exclusive with TrackInvocationFailure: whichever is called first wins.
func (t *GraphTracker) TrackInvocationSuccess() error {
	if t.success.IsSome() {
		t.logWarning("Skipping TrackInvocationSuccess: invocation already recorded on this graph tracker.")
		return nil
	}
	t.success = ldcommon.Some(true)
	return t.events.TrackMetric(graphInvocationSuccess, t.context, 1, t.trackData)
}

// TrackInvocationFailure records that the graph invocation failed.
//
// At-most-once and mutually exclusive with TrackInvocationSuccess: whichever is called first wins.
func (t *GraphTracker) TrackInvocationFailure() error {
	if t.success.IsSome() {
		t.logWarning("Skipping TrackInvocationFailure: invocation already recorded on this graph tracker.")
		return nil
	}
	t.success = ldcommon.Some(false)
	return t.events.TrackMetric(graphInvocationFailure, t.context, 1, t.trackData)
}

// TrackDuration records the total wall-clock duration of the graph invocation in milliseconds.
//
// At-most-once. Non-finite values are ignored without consuming the at-most-once slot.
func (t *GraphTracker) TrackDuration(durationMs float64) error {
	if math.IsNaN(durationMs) || math.IsInf(durationMs, 0) {
		t.logDebug("Skipping TrackDuration: durationMs is not finite (%v).", durationMs)
		return nil
	}
	if t.duration.IsSome() {
		t.logWarning("Skipping TrackDuration: duration already recorded on this graph tracker.")
		return nil
	}
	t.duration = ldcommon.Some(durationMs)
	return t.events.TrackMetric(graphDurationTotal, t.context, durationMs, t.trackData)
}

// TrackTotalTokens records the total token usage for the graph invocation.
//
// At-most-once.
func (t *GraphTracker) TrackTotalTokens(tokens TokenUsage) error {
	if t.tokens.IsSome() {
		t.logWarning("Skipping TrackTotalTokens: token usage already recorded on this graph tracker.")
		return nil
	}
	t.tokens = ldcommon.Some(tokens)
	return t.events.TrackMetric(graphTotalTokens, t.context, float64(tokens.Total), t.trackData)
}

// TrackPath records the ordered path of node keys visited during the graph invocation.
//
// At-most-once. Nil or empty paths are ignored without consuming the at-most-once slot.
func (t *GraphTracker) TrackPath(path []string) error {
	if len(path) == 0 {
		t.logDebug("Skipping TrackPath: path was nil or empty.")
		return nil
	}
	if t.path.IsSome() {
		t.logWarning("Skipping TrackPath: path already recorded on this graph tracker.")
		return nil
	}
	snapshot := make([]string, len(path))
	copy(snapshot, path)
	t.path = ldcommon.Some(snapshot)
	return t.events.TrackMetric(graphPath, t.context, 1, t.withPathData(path))
}

func (t *GraphTracker) withPathData(path []string) ldvalue.Value {
	builder := ldvalue.ObjectBuild().
		Set("runId", ldvalue.String(t.runID)).
		Set("graphKey", ldvalue.String(t.graphKey)).
		Set("version", ldvalue.Int(t.version))
	if t.variationKey != "" {
		builder.Set("variationKey", ldvalue.String(t.variationKey))
	}
	ab := ldvalue.ArrayBuildWithCapacity(len(path))
	for _, key := range path {
		ab.Add(ldvalue.String(key))
	}
	return builder.Set("path", ab.Build()).Build()
}

func (t *GraphTracker) withEdgeData(sourceKey, targetField, targetValue string) ldvalue.Value {
	builder := ldvalue.ObjectBuild().
		Set("runId", ldvalue.String(t.runID)).
		Set("graphKey", ldvalue.String(t.graphKey)).
		Set("version", ldvalue.Int(t.version))
	if t.variationKey != "" {
		builder.Set("variationKey", ldvalue.String(t.variationKey))
	}
	return builder.
		Set("sourceKey", ldvalue.String(sourceKey)).
		Set(targetField, ldvalue.String(targetValue)).
		Build()
}

// TrackRedirect records a redirect where the graph transitioned to a different target than the edge specified.
//
// Multi-fire: every call emits an event. Blank keys are ignored.
func (t *GraphTracker) TrackRedirect(sourceKey, redirectedTarget string) error {
	if strings.TrimSpace(sourceKey) == "" || strings.TrimSpace(redirectedTarget) == "" {
		t.logDebug("Skipping TrackRedirect: sourceKey or redirectedTarget was blank.")
		return nil
	}
	return t.events.TrackMetric(
		graphRedirect,
		t.context,
		1,
		t.withEdgeData(sourceKey, "redirectedTarget", redirectedTarget),
	)
}

// TrackHandoffSuccess records a successful handoff from one node to another.
//
// Multi-fire: every call emits an event. Blank keys are ignored.
func (t *GraphTracker) TrackHandoffSuccess(sourceKey, targetKey string) error {
	if strings.TrimSpace(sourceKey) == "" || strings.TrimSpace(targetKey) == "" {
		t.logDebug("Skipping TrackHandoffSuccess: sourceKey or targetKey was blank.")
		return nil
	}
	return t.events.TrackMetric(
		graphHandoffSuccess,
		t.context,
		1,
		t.withEdgeData(sourceKey, "targetKey", targetKey),
	)
}

// TrackHandoffFailure records a failed handoff from one node to another.
//
// Multi-fire: every call emits an event. Blank keys are ignored.
func (t *GraphTracker) TrackHandoffFailure(sourceKey, targetKey string) error {
	if strings.TrimSpace(sourceKey) == "" || strings.TrimSpace(targetKey) == "" {
		t.logDebug("Skipping TrackHandoffFailure: sourceKey or targetKey was blank.")
		return nil
	}
	return t.events.TrackMetric(
		graphHandoffFailure,
		t.context,
		1,
		t.withEdgeData(sourceKey, "targetKey", targetKey),
	)
}

// GetSummary returns a snapshot of graph-level metrics recorded so far.
func (t *GraphTracker) GetSummary() GraphMetricSummary {
	var path []string
	if t.path.IsSome() {
		recorded := t.path.Unwrap()
		path = make([]string, len(recorded))
		copy(path, recorded)
	}
	return GraphMetricSummary{
		Success:         t.success,
		DurationMs:      t.duration,
		Tokens:          t.tokens,
		Path:            path,
		ResumptionToken: t.ResumptionToken(),
	}
}
