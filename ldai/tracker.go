package ldai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	ldcommon "github.com/launchdarkly/go-sdk-common/v4"
	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

const (
	duration          = "$ld:ai:duration:total"
	feedbackPositive  = "$ld:ai:feedback:user:positive"
	feedbackNegative  = "$ld:ai:feedback:user:negative"
	generationSuccess = "$ld:ai:generation:success"
	generationError   = "$ld:ai:generation:error"
	//nolint:gosec
	timeToFirstToken = "$ld:ai:tokens:ttf"
	//nolint:gosec
	tokenTotal = "$ld:ai:tokens:total"
	//nolint:gosec
	tokenInput = "$ld:ai:tokens:input"
	//nolint:gosec
	tokenOutput = "$ld:ai:tokens:output"
	toolCall    = "$ld:ai:tool_call"
)

// newRunID returns a fresh UUIDv4 that LaunchDarkly uses to group all metric
// events emitted by one Tracker into a single AI run, so they can be analyzed
// together. Each call to CreateTracker on the AI Config mints a new runId.
func newRunID() string {
	return uuid.New().String()
}

// TokenUsage represents the token usage returned by a model provider for a specific request.
// It is an alias for datamodel.TokenUsage.
type TokenUsage = datamodel.TokenUsage

// MetricSummary represents a summary of metrics tracked by the tracker.
type MetricSummary struct {
	// Duration is the tracked duration in milliseconds.
	Duration ldcommon.Option[time.Duration]
	// Feedback is the tracked user feedback (positive or negative).
	Feedback ldcommon.Option[Feedback]
	// Tokens contains information about token usage.
	Tokens ldcommon.Option[TokenUsage]
	// Success indicates whether the operation was successful.
	Success ldcommon.Option[bool]
	// TimeToFirstToken is the time to the first token in milliseconds.
	TimeToFirstToken ldcommon.Option[time.Duration]
	// ToolCalls is the accumulated list of tool keys tracked on this tracker, in tracking order.
	ToolCalls []string
}

// Metrics represents the metrics returned by a model provider for a specific request.
type Metrics struct {
	// Latency is the latency of the request.
	Latency time.Duration
	// TimeToFirstToken is the time to the first token of the streamed response.
	TimeToFirstToken time.Duration
}

// AIMetrics contains the metrics for a single AI operation, as extracted from an arbitrary
// operation result by TrackMetricsOf. It is an alias for datamodel.AIMetrics.
type AIMetrics = datamodel.AIMetrics

// ProviderResponse represents the response from a model provider for a specific request.
type ProviderResponse struct {
	// Usage is the token usage.
	Usage TokenUsage
	// Metrics is the request metrics.
	Metrics Metrics
}

// Feedback represents the feedback provided by a user for a model evaluation.
type Feedback string

const (
	// FeedbackPositive is positive feedback.
	FeedbackPositive Feedback = "positive"
	// FeedbackNegative is negative feedback.
	FeedbackNegative Feedback = "negative"
)

// EventSink represents the Tracker's requirements for delivering analytic events. This is generally satisfied
// by the LaunchDarkly SDK's TrackMetric method.
type EventSink interface {
	// TrackMetric sends a named analytic event to LaunchDarkly relevant to a particular context, and containing a
	// metric value and additional data.
	TrackMetric(
		eventName string,
		context ldcontext.Context,
		metricValue float64,
		data ldvalue.Value,
	) error
}

// Stopwatch is used to measure the duration of a task. Start will always be called before Stop.
// If an implementation is not provided, the Tracker uses a default implementation that delegates to
// time.Now and time.Since.
type Stopwatch interface {
	// Start starts the stopwatch.
	Start()
	// Stop stops the stopwatch and returns the duration since Start was called.
	Stop() time.Duration
}

// resumptionPayload is the JSON structure encoded into a resumption token.
type resumptionPayload struct {
	RunID        string `json:"runId"`
	ConfigKey    string `json:"configKey"`
	VariationKey string `json:"variationKey,omitempty"`
	Version      int    `json:"version"`
}

// Tracker records metrics for a single AI run.
// Unless otherwise noted, the Tracker's methods are not safe for concurrent use.
//
// All events a Tracker emits share a runId (a UUIDv4) so LaunchDarkly can
// correlate them in metrics views. See individual track methods for their
// specific semantics. Call CreateTracker on the AI Config to start a new run.
// A ResumptionToken preserves the runId, so events emitted by a Tracker
// reconstructed in another process correlate with the original run.
type Tracker struct {
	key          string
	runID        string
	variationKey string
	version      int
	config       *Config
	context      ldcontext.Context
	events       EventSink
	trackData    ldvalue.Value
	logger       interfaces.LDLoggers
	stopwatch    Stopwatch

	duration         ldcommon.Option[time.Duration]
	feedback         ldcommon.Option[Feedback]
	tokens           ldcommon.Option[TokenUsage]
	success          ldcommon.Option[bool]
	timeToFirstToken ldcommon.Option[time.Duration]
	toolCalls        []string
}

// Used if a custom Stopwatch is not provided.
type defaultStopwatch struct {
	start time.Time
}

// Start saves the current time using time.Now.
func (d *defaultStopwatch) Start() {
	d.start = time.Now()
}

// Stop returns the duration since Start was called using time.Since.
func (d *defaultStopwatch) Stop() time.Duration {
	return time.Since(d.start)
}

// newTracker creates a new Tracker with the specified runID, key, event sink, config, context, and loggers.
func newTracker(
	events EventSink,
	runID string,
	key string,
	variationKey string,
	version int,
	ctx ldcontext.Context,
	config *Config,
	loggers interfaces.LDLoggers,
) *Tracker {
	return newTrackerWithStopwatch(events, runID, key, variationKey, version, ctx, config, loggers, &defaultStopwatch{})
}

// newTrackerWithStopwatch creates a new Tracker with the specified runID, key, event sink, config, context, loggers,
// and stopwatch. This method is used for testing purposes.
func newTrackerWithStopwatch(
	events EventSink,
	runID string,
	key string,
	variationKey string,
	version int,
	ctx ldcontext.Context,
	config *Config,
	loggers interfaces.LDLoggers,
	stopwatch Stopwatch,
) *Tracker {
	if config == nil {
		panic("LaunchDarkly SDK programmer error: config must never be nil")
	}

	builder := ldvalue.ObjectBuild().
		Set("runId", ldvalue.String(runID)).
		Set("configKey", ldvalue.String(key)).
		Set("version", ldvalue.Int(version)).
		Set("providerName", ldvalue.String(config.ProviderName())).
		Set("modelName", ldvalue.String(config.ModelName())).
		Set("aiSdkName", ldvalue.String(SDKName)).
		Set("aiSdkVersion", ldvalue.String(Version))
	if variationKey != "" {
		builder.Set("variationKey", ldvalue.String(variationKey))
	}
	trackData := builder.Build()

	return &Tracker{
		key:          key,
		runID:        runID,
		variationKey: variationKey,
		version:      version,
		config:       config,
		trackData:    trackData,
		events:       events,
		context:      ctx,
		logger:       loggers,
		stopwatch:    stopwatch,
	}
}

func (t *Tracker) logWarning(format string, args ...interface{}) {
	prefix := "AI Config tracker for '" + t.key + "': "
	t.logger.Warnf(prefix+format, args...)
}

// ResumptionToken returns a URL-safe Base64-encoded token that can be used to reconstruct a tracker
// in a different process (e.g., for deferred feedback). The token contains the runId, configKey,
// variationKey, and version. It does not contain modelName or providerName.
func (t *Tracker) ResumptionToken() string {
	payload := resumptionPayload{
		RunID:        t.runID,
		ConfigKey:    t.key,
		VariationKey: t.variationKey,
		Version:      t.version,
	}
	jsonBytes, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(jsonBytes)
}

// TrackerFromResumptionToken reconstructs a Tracker from a resumption token and the given context.
// This is used for cross-process scenarios (e.g., deferred feedback) where the original tracker
// is no longer available but its runId must be reused. The token is obtained from Tracker.ResumptionToken().
// The reconstructed tracker will have empty modelName and providerName since these are not included
// in the token.
func TrackerFromResumptionToken(token string, sdk ServerSDK, context ldcontext.Context) (*Tracker, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid resumption token: %w", err)
	}
	var payload resumptionPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("invalid resumption token: %w", err)
	}

	emptyConfig := Disabled()
	return newTracker(
		sdk,
		payload.RunID,
		payload.ConfigKey,
		payload.VariationKey,
		payload.Version,
		context,
		&emptyConfig,
		sdk.Loggers(),
	), nil
}

// TrackDuration tracks the duration of a task. For example, the duration of a model evaluation request may be
// tracked here. See also TrackRequest.
// The duration in milliseconds must fit within a float64.
//
// Records at most once per Tracker; further calls are ignored.
func (t *Tracker) TrackDuration(dur time.Duration) error {
	if t.duration.IsSome() {
		t.logWarning("Skipping TrackDuration: duration already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}
	t.duration = ldcommon.Some(dur)
	return t.events.TrackMetric(duration, t.context, float64(dur.Milliseconds()), t.trackData)
}

// TrackFeedback tracks the feedback provided by a user for a model evaluation. If the feedback is not
// FeedbackPositive or FeedbackNegative, returns an error and does not track anything.
//
// Records at most once per Tracker; further calls are ignored.
func (t *Tracker) TrackFeedback(feedback Feedback) error {
	if t.feedback.IsSome() {
		t.logWarning("Skipping TrackFeedback: feedback already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}
	switch feedback {
	case FeedbackPositive:
		t.feedback = ldcommon.Some(feedback)
		return t.events.TrackMetric(feedbackPositive, t.context, 1, t.trackData)
	case FeedbackNegative:
		t.feedback = ldcommon.Some(feedback)
		return t.events.TrackMetric(feedbackNegative, t.context, 1, t.trackData)
	default:
		return fmt.Errorf("tracker: unexpected feedback value: %v", feedback)
	}
}

// TrackSuccess tracks a successful model evaluation.
//
// Records at most once per Tracker. TrackSuccess and TrackError share state;
// only one of the two can record per Tracker, and subsequent calls are ignored.
func (t *Tracker) TrackSuccess() error {
	if t.success.IsSome() {
		t.logWarning("Skipping TrackSuccess: success/error already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}
	t.success = ldcommon.Some(true)

	return t.events.TrackMetric(generationSuccess, t.context, 1, t.trackData)
}

// TrackError tracks an unsuccessful model evaluation.
//
// Records at most once per Tracker. TrackSuccess and TrackError share state;
// only one of the two can record per Tracker, and subsequent calls are ignored.
func (t *Tracker) TrackError() error {
	if t.success.IsSome() {
		t.logWarning("Skipping TrackError: success/error already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}
	t.success = ldcommon.Some(false)

	return t.events.TrackMetric(generationError, t.context, 1, t.trackData)
}

// TrackTimeToFirstToken tracks the time to the first token of the streamed response.
//
// Records at most once per Tracker; further calls are ignored.
func (t *Tracker) TrackTimeToFirstToken(dur time.Duration) error {
	if t.timeToFirstToken.IsSome() {
		t.logWarning("Skipping TrackTimeToFirstToken: time-to-first-token already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}
	t.timeToFirstToken = ldcommon.Some(dur)
	return t.events.TrackMetric(timeToFirstToken, t.context, float64(dur.Milliseconds()), t.trackData)
}

// TrackTokens tracks the token usage for a model evaluation.
//
// Records at most once per Tracker; further calls are ignored.
func (t *Tracker) TrackTokens(usage TokenUsage) error {
	if t.tokens.IsSome() {
		t.logWarning("Skipping TrackTokens: token usage already recorded on this tracker. "+
			"Call CreateTracker on the AI Config for a new run. %s", t.trackData.JSONString())
		return nil
	}

	if usage.Set() {
		t.tokens = ldcommon.Some(usage)
	}

	var failed bool

	if usage.Total > 0 {
		if err1 := t.events.TrackMetric(tokenTotal, t.context, float64(usage.Total), t.trackData); err1 != nil {
			t.logWarning("error tracking total token usage: %v", err1)
			failed = true
		}
	}
	if usage.Input > 0 {
		if err2 := t.events.TrackMetric(tokenInput, t.context, float64(usage.Input), t.trackData); err2 != nil {
			t.logWarning("error tracking input token usage: %v", err2)
			failed = true
		}
	}
	if usage.Output > 0 {
		if err3 := t.events.TrackMetric(tokenOutput, t.context, float64(usage.Output), t.trackData); err3 != nil {
			t.logWarning("error tracking output token usage: %v", err3)
			failed = true
		}
	}

	if failed {
		return fmt.Errorf("tracker: error tracking token usage, logs contain more information")
	}

	return nil
}

// TrackUsage tracks token usage.
//
// Deprecated: Use TrackTokens instead.
func (t *Tracker) TrackUsage(usage TokenUsage) error {
	return t.TrackTokens(usage)
}

// TrackToolCall tracks a tool call made during an AI run.
//
// May be called multiple times per Tracker; each call records a tool call event for the provided
// tool key and appends it to the summary.
func (t *Tracker) TrackToolCall(toolKey string) error {
	t.toolCalls = append(t.toolCalls, toolKey)
	data := t.trackDataWith("toolKey", ldvalue.String(toolKey))
	return t.events.TrackMetric(toolCall, t.context, 1, data)
}

// TrackToolCalls tracks the tool calls made during an AI run, recording one event per tool key.
//
// May be called multiple times per Tracker; tool keys accumulate in the summary.
func (t *Tracker) TrackToolCalls(toolKeys []string) error {
	var failed bool
	for _, toolKey := range toolKeys {
		if err := t.TrackToolCall(toolKey); err != nil {
			t.logWarning("error tracking tool call %q: %v", toolKey, err)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("tracker: error tracking tool calls, logs contain more information")
	}
	return nil
}

// trackDataWith returns the tracker's event data with one additional field merged in.
func (t *Tracker) trackDataWith(key string, value ldvalue.Value) ldvalue.Value {
	builder := ldvalue.ObjectBuild()
	for _, k := range t.trackData.Keys(nil) {
		builder.Set(k, t.trackData.GetByKey(k))
	}
	return builder.Set(key, value).Build()
}

// GetSummary returns a summary of all metrics that have been tracked using this tracker.
func (t *Tracker) GetSummary() MetricSummary {
	return MetricSummary{
		Duration:         t.duration,
		Feedback:         t.feedback,
		Tokens:           t.tokens,
		Success:          t.success,
		TimeToFirstToken: t.timeToFirstToken,
		ToolCalls:        slices.Clone(t.toolCalls),
	}
}

// logTrackErr logs a delivery failure from an inner Track call made on behalf of a wrapper
// (TrackMetricsOf, TrackRequest). Wrappers log these rather than failing the whole operation.
func (t *Tracker) logTrackErr(what string, err error) {
	if err != nil {
		t.logWarning("error tracking %s metric for operation: %v", what, err)
	}
}

// extractMetrics calls metricsExtractor, recovering a panic into nil metrics (with a logged
// warning) so a metrics-extraction bug degrades to duration-only tracking instead of losing the
// whole operation's metrics. This mirrors the Python SDK, which catches extractor exceptions.
func extractMetrics[T any](t *Tracker, metricsExtractor func(T) *AIMetrics, result T) (metrics *AIMetrics) {
	defer func() {
		if r := recover(); r != nil {
			t.logWarning("panic extracting metrics from operation result: %v", r)
			metrics = nil
		}
	}()
	return metricsExtractor(result)
}

// TrackMetricsOf runs task and tracks the metrics of the resulting AI operation, without
// requiring the operation to be tied to the tracker's AI Config (compare TrackRequest, which is
// implemented in terms of this function). It is a package-level function because Go does not
// permit type parameters on methods.
//
// The task's duration is always tracked, even if the task panics (the panic is then propagated).
// If the task returns an error, an unsuccessful generation is tracked and the error is returned.
// Otherwise metricsExtractor derives metrics from the task's result: a non-zero
// AIMetrics.Duration replaces the wall-clock measurement, success or error is tracked per
// AIMetrics.Success, and any time to first token, token usage, and tool calls present are
// tracked. A nil *AIMetrics from the extractor — or a nil metricsExtractor — tracks the duration
// only. A panic in the extractor is recovered and logged, degrading to duration-only tracking.
//
// Subsequent calls re-run the task, but at-most-once metrics already recorded on the tracker are
// not emitted again (tool call events are multi-fire).
func TrackMetricsOf[T any](
	t *Tracker,
	metricsExtractor func(T) *AIMetrics,
	task func() (T, error),
) (T, error) {
	t.stopwatch.Start()
	completed := false
	defer func() {
		if !completed { // the task panicked; record what we know, then let the panic propagate
			t.logTrackErr("duration", t.TrackDuration(t.stopwatch.Stop()))
			t.logTrackErr("error", t.TrackError())
		}
	}()
	result, err := task()
	elapsed := t.stopwatch.Stop()
	completed = true

	var metrics *AIMetrics
	if err == nil && metricsExtractor != nil {
		metrics = extractMetrics(t, metricsExtractor, result)
	}

	trackedDuration := elapsed
	if metrics != nil && metrics.Duration != 0 {
		trackedDuration = metrics.Duration
	}
	t.logTrackErr("duration", t.TrackDuration(trackedDuration))

	if err != nil {
		t.logTrackErr("error", t.TrackError())
		return result, err
	}
	if metrics == nil {
		return result, nil
	}

	if metrics.Success {
		t.logTrackErr("success", t.TrackSuccess())
	} else {
		t.logTrackErr("error", t.TrackError())
	}

	if metrics.TimeToFirstToken != 0 {
		t.logTrackErr("time to first token", t.TrackTimeToFirstToken(metrics.TimeToFirstToken))
	}

	if metrics.Tokens.Set() {
		// TrackTokens logs errors.
		_ = t.TrackTokens(metrics.Tokens)
	}

	if len(metrics.ToolCalls) > 0 {
		// TrackToolCalls logs errors.
		_ = t.TrackToolCalls(metrics.ToolCalls)
	}

	return result, nil
}

// TrackRequest tracks metrics for a model evaluation request. It is a convenience form of
// TrackMetricsOf for tasks that work with the tracker's AI Config and report a ProviderResponse:
// the task receives the current AI Config, and all fields of the returned ProviderResponse are
// optional.
//
// The request's duration is always tracked, using the ProviderResponse's Latency when set and an
// automatically measured duration otherwise. If the task returns an error, an unsuccessful
// generation is tracked and the error is returned with a zero ProviderResponse. Otherwise a
// successful generation is tracked along with any time to first token and token usage set in the
// ProviderResponse.
//
// Subsequent calls re-run the task but emit only metrics not already recorded
// on this Tracker. Call CreateTracker on the AI Config to start a new run.
func (t *Tracker) TrackRequest(task func(c *Config) (ProviderResponse, error)) (ProviderResponse, error) {
	response, err := TrackMetricsOf(t, func(r ProviderResponse) *AIMetrics {
		return &AIMetrics{
			Success:          true,
			Tokens:           r.Usage,
			Duration:         r.Metrics.Latency,
			TimeToFirstToken: r.Metrics.TimeToFirstToken,
		}
	}, func() (ProviderResponse, error) {
		return task(t.config)
	})
	if err != nil {
		t.logWarning("error executing request: %v", err)
		return ProviderResponse{}, err
	}
	return response, nil
}

// TrackJudgeResponse tracks the evaluation scores from a judge response.
//
// May be called multiple times per Tracker; each call records the scores from
// the given response.
func (t *Tracker) TrackJudgeResponse(response datamodel.JudgeResponse) error {
	if !response.Success {
		return nil
	}

	// Build the data object once, since it's constant across all iterations
	data := t.trackData
	if response.JudgeConfigKey != "" {
		data = t.trackDataWith("judgeConfigKey", ldvalue.String(response.JudgeConfigKey))
	}

	var failed bool
	for metricKey, evalScore := range response.Evals {
		if err := t.events.TrackMetric(metricKey, t.context, evalScore.Score, data); err != nil {
			t.logWarning("error tracking metric %s: %v", metricKey, err)
			failed = true
		}
	}

	if failed {
		return fmt.Errorf("error tracking evaluation scores")
	}
	return nil
}
