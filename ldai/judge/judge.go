package judge

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"github.com/launchdarkly/go-server-sdk-ai/ldai"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// Config defines the subset of an AI Config that a Judge requires.
// It is satisfied by *ldai.AIJudgeConfig (primary) and *ldai.AICompletionConfig (deprecated).
type Config interface {
	Messages() []datamodel.Message
	EvaluationMetricKey() string
}

// configWithLegacyMetricKeys is an optional extension of Config for types that carry the
// deprecated evaluationMetricKeys array. Checked via type assertion in getMetricKey.
type configWithLegacyMetricKeys interface {
	EvaluationMetricKeys() []string
}

// Tracker defines the metric-tracking operations a Judge requires. This is satisfied by *ldai.Tracker.
type Tracker interface {
	TrackJudgeResponse(response datamodel.JudgeResponse) error
	TrackTokens(usage ldai.TokenUsage) error
}

// Compile-time assertions that the ldai package's types satisfy these interfaces.
var (
	_ Config  = (*ldai.AIJudgeConfig)(nil)      // primary implementation
	_ Config  = (*ldai.AICompletionConfig)(nil) // deprecated Config alias remains compatible
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
}

// New creates a Judge from a judge AI Config, a tracker for reporting metrics, and a model provider.
// The config, tracker, and provider must not be nil, and the config must define an evaluation metric key.
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
	}, nil
}

// Evaluate runs the judge against the given input (e.g., the prompt or message history) and output
// (the response to evaluate). The evaluation is sampled at the given rate (0.0-1.0); a nil response
// with a nil error indicates the evaluation was skipped due to sampling or an empty judge config.
func (j *Judge) Evaluate(input, output string, samplingRate float64) (*datamodel.JudgeResponse, error) {
	if len(j.config.Messages()) == 0 {
		return nil, nil
	}

	//nolint:gosec // sampling does not require cryptographic randomness
	if samplingRate < 1.0 && rand.Float64() > samplingRate {
		return nil, nil
	}

	messages := j.buildMessages(input, output)
	schema := buildSchema(j.metricKey)

	response, err := j.provider.InvokeStructuredModel(messages, schema)
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

// EvaluateMessages is a convenience form of Evaluate that joins the given messages' contents into
// a single input string.
func (j *Judge) EvaluateMessages(
	messages []datamodel.Message,
	response string,
	samplingRate float64,
) (*datamodel.JudgeResponse, error) {
	parts := make([]string, len(messages))
	for i, msg := range messages {
		parts[i] = msg.Content
	}
	input := strings.Join(parts, "\r\n")
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

func (j *Judge) buildMessages(input, output string) []datamodel.Message {
	// Use string replacement to prevent context attributes like {{=[ ]=}}) from
	// influencing judge template parsing.
	replacer := strings.NewReplacer(
		ldai.JudgePlaceholderMessageHistory, input,
		ldai.JudgePlaceholderResponseToEvaluate, output,
	)

	messages := j.config.Messages()
	result := make([]datamodel.Message, len(messages))

	for i, msg := range messages {
		result[i] = datamodel.Message{Content: replacer.Replace(msg.Content), Role: msg.Role}
	}

	return result
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
// score (plain JSON decoding, json.Decoder.UseNumber, or a hand-built map).
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func (j *Judge) parseResponse(data map[string]interface{}) *datamodel.JudgeResponse {
	evaluations, ok := data["evaluations"].(map[string]interface{})
	if !ok {
		return j.failureResponse("missing evaluations object")
	}

	evalData, ok := evaluations[j.metricKey].(map[string]interface{})
	if !ok {
		return j.failureResponse(fmt.Sprintf("missing evaluation for %s", j.metricKey))
	}

	score, ok := toFloat64(evalData["score"])
	if !ok || score < 0 || score > 1 {
		return j.failureResponse("invalid score")
	}

	reasoning, ok := evalData["reasoning"].(string)
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
	// Priority 1: primary evaluationMetricKey field.
	if metricKey := config.EvaluationMetricKey(); strings.TrimSpace(metricKey) != "" {
		return strings.TrimSpace(metricKey), nil
	}

	// Priority 2: deprecated evaluationMetricKeys array (optional interface).
	if lc, ok := config.(configWithLegacyMetricKeys); ok {
		for _, key := range lc.EvaluationMetricKeys() {
			if trimmed := strings.TrimSpace(key); trimmed != "" {
				if logger != nil {
					logger.Warnf("Judge config %q: using deprecated evaluationMetricKeys; use evaluationMetricKey instead", configKey)
				}
				return trimmed, nil
			}
		}
	}

	if configKey != "" {
		return "", fmt.Errorf("judge config %q: missing evaluationMetricKey", configKey)
	}
	return "", fmt.Errorf("missing evaluationMetricKey")
}
