package ldai

import (
	"slices"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// AICompletionConfig represents a completion-mode AI Config retrieved from LaunchDarkly.
// It provides access to model parameters, provider details, messages, tools, and judge
// configuration. Use Client.CompletionConfig to obtain an instance.
//
// To send analytic events to LaunchDarkly, call CreateTracker to obtain a Tracker.
type AICompletionConfig struct {
	aiConfigBase
	messages             []datamodel.Message
	judgeConfiguration   *datamodel.JudgeConfiguration
	evaluationMetricKey  string
	evaluationMetricKeys []string
	// raw is the underlying wire representation, kept for AsLdValue() marshaling.
	raw datamodel.Config
}

// Messages returns the interpolated messages defined by the config. The series of messages
// may be passed to an AI model provider.
func (c *AICompletionConfig) Messages() []datamodel.Message {
	return slices.Clone(c.messages)
}

// JudgeConfiguration returns the judge configuration attached to this config, if any.
// Returns a defensive copy to prevent mutations.
func (c *AICompletionConfig) JudgeConfiguration() *datamodel.JudgeConfiguration {
	return c.judgeConfiguration.Clone()
}

// AsLdValue is used internally.
func (c *AICompletionConfig) AsLdValue() ldvalue.Value {
	return ldvalue.FromJSONMarshal(c.raw)
}

// VariationKey is used internally by LaunchDarkly.
//
// Deprecated: This is an internal implementation detail.
func (c *AICompletionConfig) VariationKey() string {
	return c.variationKey
}

// Version is used internally by LaunchDarkly.
//
// Deprecated: This is an internal implementation detail.
func (c *AICompletionConfig) Version() int {
	return c.version
}

// ModelName returns the model name associated with the config.
//
// Deprecated: Use Model().Name instead.
func (c *AICompletionConfig) ModelName() string {
	return c.model.Name
}

// ProviderName returns the provider name associated with the config.
//
// Deprecated: Use Provider().Name instead.
func (c *AICompletionConfig) ProviderName() string {
	return c.provider.Name
}

// ModelParam returns the model parameter named by key. The second return value is true if
// the key exists.
//
// Deprecated: Use Model().Parameters instead.
func (c *AICompletionConfig) ModelParam(key string) (ldvalue.Value, bool) {
	val, ok := c.model.Parameters[key]
	return val, ok
}

// CustomModelParam returns the custom model parameter named by key. The second return value
// is true if the key exists.
//
// Deprecated: Use Model().Custom instead.
func (c *AICompletionConfig) CustomModelParam(key string) (ldvalue.Value, bool) {
	val, ok := c.model.Custom[key]
	return val, ok
}

// Mode returns the AI Config mode (e.g., "completion", "agent", "judge").
//
// Deprecated: The config type itself indicates the mode.
func (c *AICompletionConfig) Mode() string {
	return c.raw.Meta.Mode
}

// EvaluationMetricKey returns the evaluation metric key for judge mode configs.
//
// Deprecated: Use AIJudgeConfig.EvaluationMetricKey instead.
func (c *AICompletionConfig) EvaluationMetricKey() string {
	return c.evaluationMetricKey
}

// EvaluationMetricKeys returns the deprecated array of evaluation metric keys.
//
// Deprecated: Use EvaluationMetricKey instead.
func (c *AICompletionConfig) EvaluationMetricKeys() []string {
	return slices.Clone(c.evaluationMetricKeys)
}
