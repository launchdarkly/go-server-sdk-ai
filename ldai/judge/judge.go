package judge

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// Config defines the subset of an AI Config that a Judge requires. This is satisfied by *ldai.Config.
type Config interface {
	Enabled() bool
	Messages() []datamodel.Message
	ModelParam(key string) (ldvalue.Value, bool)
	CustomModelParam(key string) (ldvalue.Value, bool)
	EvaluationMetricKey() string
	EvaluationMetricKeys() []string
}

// Tracker defines the metric-tracking operations a Judge requires. This is satisfied by *ldai.Tracker.
type Tracker interface {
	TrackJudgeResponse(response datamodel.JudgeResponse) error
	TrackTokens(usage ldai.TokenUsage) error
}

// Compile-time assertions that the ldai package's types satisfy these interfaces.
var (
	_ Config  = (*ldai.Config)(nil)
	_ Tracker = (*ldai.Tracker)(nil)
)

// StructuredResponse is the result of a structured model invocation: the parsed JSON content and
// the token usage reported by the provider.
type StructuredResponse struct {
	Content map[string]interface{}
	Usage   ldai.TokenUsage
}

// Provider defines the model invocation operations a Judge requires. Implementations are supplied
// by the application, typically wrapping an AI model provider's SDK.
type Provider interface {
	InvokeStructuredModel(messages []datamodel.Message, schema map[string]interface{}) (StructuredResponse, error)
}

// Judge evaluates AI-generated responses against a judge AI Config, producing scored evaluations
// that can be tracked to LaunchDarkly.
type Judge struct {
	config         Config
	tracker        Tracker
	provider       Provider
	metricKey      string
	judgeConfigKey string
	logger         interfaces.LDLoggers
	// messages and schema are snapshotted at construction: the config's messages with legacy
	// placeholder template messages stripped, and the fixed evaluation schema.
	messages []datamodel.Message
	schema   map[string]interface{}
}

// New creates a Judge from a judge AI Config, a tracker for reporting metrics, and a model provider.
// The config, tracker, and provider must not be nil; the config must be enabled and define an
// evaluation metric key. The config's messages are snapshotted at construction.
func New(
	config Config,
	tracker Tracker,
	provider Provider,
	configKey string,
	logger interfaces.LDLoggers,
) (*Judge, error) {
	if config == nil {
		return nil, fmt.Errorf("config must not be nil")
	}
	if tracker == nil {
		return nil, fmt.Errorf("tracker must not be nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("provider must not be nil")
	}
	if !config.Enabled() {
		return nil, fmt.Errorf("judge config %q is disabled", configKey)
	}

	metricKey, err := getMetricKey(config, logger, configKey)
	if err != nil {
		return nil, err
	}

	return &Judge{
		config:         config,
		tracker:        tracker,
		provider:       provider,
		metricKey:      metricKey,
		judgeConfigKey: configKey,
		logger:         logger,
		messages:       ldai.StripLegacyJudgeMessages(config.Messages()),
		schema:         buildSchema(),
	}, nil
}

// Evaluate runs the judge against the given input (e.g., the prompt or message history) and output
// (the response to evaluate). The evaluation is sampled at the given rate (0.0-1.0); a nil response
// with a nil error indicates the evaluation was skipped due to sampling.
func (j *Judge) Evaluate(input, output string, samplingRate float64) (*datamodel.JudgeResponse, error) {
	//nolint:gosec // sampling does not require cryptographic randomness
	if samplingRate < 1.0 && rand.Float64() > samplingRate {
		return nil, nil
	}

	messages := j.buildMessages(input, output)

	response, err := j.provider.InvokeStructuredModel(messages, j.schema)
	if err != nil {
		return j.failureResponse(err.Error()), nil
	}

	if response.Usage.Set() {
		_ = j.tracker.TrackTokens(response.Usage)
	}

	result := j.parseResponse(response.Content)
	// Note: Judge response tracking should be done by the caller (AI config being evaluated)
	// not by the judge itself. This matches Python and JavaScript SDK behavior.

	return result, nil
}

// EvaluateMessages is a convenience form of Evaluate that renders the given messages as the
// evaluation input. Each message is rendered as "<role>: <content>" and joined with newlines, so
// the judge model can distinguish speakers in the message history.
func (j *Judge) EvaluateMessages(
	messages []datamodel.Message,
	response string,
	samplingRate float64,
) (*datamodel.JudgeResponse, error) {
	parts := make([]string, len(messages))
	for i, msg := range messages {
		parts[i] = string(msg.Role) + ": " + msg.Content
	}
	input := strings.Join(parts, "\n")
	return j.Evaluate(input, response, samplingRate)
}

// GetConfig returns the judge's AI Config.
func (j *Judge) GetConfig() Config {
	return j.config
}

// GetTracker returns the judge's tracker.
func (j *Judge) GetTracker() Tracker {
	return j.tracker
}

// GetProvider returns the judge's model provider.
func (j *Judge) GetProvider() Provider {
	return j.provider
}

// buildMessages constructs the messages sent to the provider: the judge's snapshotted config
// messages (with legacy placeholder template messages stripped) followed by a user message
// containing the evaluation input. The evaluated conversation is never substituted into config
// messages, so model-generated or user-controlled content cannot alter the judge's prompt
// structure.
func (j *Judge) buildMessages(input, output string) []datamodel.Message {
	messages := make([]datamodel.Message, len(j.messages), len(j.messages)+1)
	copy(messages, j.messages)
	return append(messages, datamodel.Message{
		Role:    datamodel.User,
		Content: buildEvaluationInput(input, output),
	})
}

// buildEvaluationInput combines the message history and the response under evaluation into the
// fixed plain-text format shared by the LaunchDarkly AI SDKs.
func buildEvaluationInput(input, output string) string {
	return "MESSAGE HISTORY:\n" + input + "\n\nRESPONSE TO EVALUATE:\n" + output
}

// failureResponse builds the JudgeResponse shape shared by all evaluation failure paths.
func (j *Judge) failureResponse(errMsg string) *datamodel.JudgeResponse {
	return &datamodel.JudgeResponse{
		Evals:          map[string]datamodel.EvalScore{},
		Success:        false,
		Error:          errMsg,
		JudgeConfigKey: j.judgeConfigKey,
	}
}

// toFloat64 coerces the numeric types a Provider implementation may realistically produce for a
// score (plain JSON decoding, json.Decoder.UseNumber, or a hand-built map). NaN and infinities
// are rejected: range checks cannot catch NaN, and neither survives JSON encoding.
func toFloat64(v interface{}) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseResponse parses the structured evaluation response, expecting top-level score and
// reasoning fields. The parsed result is keyed by the judge's evaluation metric key so that
// tracked eval-score events use the configured metric key.
func (j *Judge) parseResponse(data map[string]interface{}) *datamodel.JudgeResponse {
	score, ok := toFloat64(data["score"])
	if !ok || score < 0 || score > 1 {
		return j.failureResponse("invalid score")
	}

	reasoning, ok := data["reasoning"].(string)
	if !ok {
		return j.failureResponse("invalid reasoning")
	}

	return &datamodel.JudgeResponse{
		Evals: map[string]datamodel.EvalScore{
			j.metricKey: {
				Score:     score,
				Reasoning: reasoning,
			},
		},
		Success:        true,
		JudgeConfigKey: j.judgeConfigKey,
	}
}

func getMetricKey(config Config, logger interfaces.LDLoggers, configKey string) (string, error) {
	// Priority 1: Check top-level evaluationMetricKey field (primary field)
	if metricKey := config.EvaluationMetricKey(); strings.TrimSpace(metricKey) != "" {
		return strings.TrimSpace(metricKey), nil
	}

	// Priority 2: Check top-level evaluationMetricKeys array (deprecated)
	keys := config.EvaluationMetricKeys()
	for _, key := range keys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			if logger != nil {
				logger.Warnf("Judge config %q: using deprecated evaluationMetricKeys; use evaluationMetricKey instead", configKey)
			}
			return trimmed, nil
		}
	}

	if configKey != "" {
		return "", fmt.Errorf("judge config %q: missing evaluationMetricKey", configKey)
	}
	return "", fmt.Errorf("missing evaluationMetricKey")
}
