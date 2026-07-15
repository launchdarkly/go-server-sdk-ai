package ldai

import (
	"maps"
	"slices"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// Config is an AI Config returned from the LaunchDarkly client.
//
// Deprecated: Use AICompletionConfig, AIAgentConfig, or AIJudgeConfig instead.
type Config = AICompletionConfig

// ConfigBuilder is used to define a default AI Config, returned when LaunchDarkly is
// unreachable or there is an error evaluating the Config.
//
// Deprecated: Use AICompletionConfigDefault instead.
type ConfigBuilder struct {
	messages             []datamodel.Message
	enabled              bool
	providerName         string
	modelName            string
	modelParams          map[string]ldvalue.Value
	modelCustomParams    map[string]ldvalue.Value
	mode                 string
	evaluationMetricKey  string
	evaluationMetricKeys []string
	judgeConfiguration   *datamodel.JudgeConfiguration
}

// NewConfig returns a new ConfigBuilder. By default, the Config is disabled.
//
// Deprecated: Use NewAICompletionConfigDefault instead.
func NewConfig() *ConfigBuilder {
	return &ConfigBuilder{
		modelParams:       make(map[string]ldvalue.Value),
		modelCustomParams: make(map[string]ldvalue.Value),
	}
}

// Disabled returns an AICompletionConfigDefault that is disabled and contains no messages.
// It is a convenience constructor equivalent to NewAICompletionConfigDefault().Disabled().
//
// Deprecated: Use NewAICompletionConfigDefault().Disabled() instead.
func Disabled() AICompletionConfigDefault {
	return NewAICompletionConfigDefault().Disabled()
}

// Config is a deprecated alias for Client.CompletionConfig. Use CompletionConfig instead.
//
// Deprecated: Use Client.CompletionConfig instead.
func (c *Client) Config(
	key string,
	context ldcontext.Context,
	defaultValue AICompletionConfigDefault,
	variables map[string]interface{},
) Config {
	return c.CompletionConfig(key, context, defaultValue, variables)
}

// WithMessage appends a message to the config with the given role.
func (cb *ConfigBuilder) WithMessage(content string, role datamodel.Role) *ConfigBuilder {
	cb.messages = append(cb.messages, datamodel.Message{
		Content: content,
		Role:    role,
	})
	return cb
}

// WithEnabled sets whether the config is enabled. See also Enable and Disable.
func (cb *ConfigBuilder) WithEnabled(enabled bool) *ConfigBuilder {
	cb.enabled = enabled
	return cb
}

// Enable enables the config.
func (cb *ConfigBuilder) Enable() *ConfigBuilder {
	return cb.WithEnabled(true)
}

// Disable disables the config.
func (cb *ConfigBuilder) Disable() *ConfigBuilder {
	return cb.WithEnabled(false)
}

// WithModelName sets the model name associated with the config.
func (cb *ConfigBuilder) WithModelName(modelName string) *ConfigBuilder {
	cb.modelName = modelName
	return cb
}

// WithProviderName sets the provider name associated with the config.
func (cb *ConfigBuilder) WithProviderName(providerName string) *ConfigBuilder {
	cb.providerName = providerName
	return cb
}

// WithModelParam sets a model parameter named by key to the given value. If the key already
// exists, it will be overwritten. Model parameters are generally set by LaunchDarkly; for
// custom parameters not recognized by LaunchDarkly, use WithCustomModelParam.
func (cb *ConfigBuilder) WithModelParam(key string, value ldvalue.Value) *ConfigBuilder {
	cb.modelParams[key] = value
	return cb
}

// WithCustomModelParam sets a custom model parameter named by key to the given value. If the
// key already exists, it will be overwritten.
func (cb *ConfigBuilder) WithCustomModelParam(key string, value ldvalue.Value) *ConfigBuilder {
	cb.modelCustomParams[key] = value
	return cb
}

// WithMode sets the AI Config mode (e.g., "completion", "agent", "judge").
func (cb *ConfigBuilder) WithMode(mode string) *ConfigBuilder {
	cb.mode = mode
	return cb
}

// WithEvaluationMetricKey sets the evaluation metric key for judge mode configs.
func (cb *ConfigBuilder) WithEvaluationMetricKey(key string) *ConfigBuilder {
	cb.evaluationMetricKey = key
	return cb
}

// WithEvaluationMetricKeys sets the deprecated array of evaluation metric keys.
// Use WithEvaluationMetricKey instead.
func (cb *ConfigBuilder) WithEvaluationMetricKeys(keys []string) *ConfigBuilder {
	cb.evaluationMetricKeys = slices.Clone(keys)
	return cb
}

// WithJudgeConfiguration sets the judge configuration for this config.
// The provided judgeConfig is defensively copied.
func (cb *ConfigBuilder) WithJudgeConfiguration(judgeConfig *datamodel.JudgeConfiguration) *ConfigBuilder {
	cb.judgeConfiguration = judgeConfig.Clone()
	return cb
}

// Build creates a Config from the current builder state.
func (cb *ConfigBuilder) Build() Config {
	raw := datamodel.Config{
		Messages: slices.Clone(cb.messages),
		Meta: datamodel.Meta{
			Enabled: cb.enabled,
			Mode:    cb.mode,
		},
		Model: datamodel.Model{
			Name:       cb.modelName,
			Parameters: maps.Clone(cb.modelParams),
			Custom:     maps.Clone(cb.modelCustomParams),
		},
		Provider: datamodel.Provider{
			Name: cb.providerName,
		},
		EvaluationMetricKey:  cb.evaluationMetricKey,
		EvaluationMetricKeys: slices.Clone(cb.evaluationMetricKeys),
		JudgeConfiguration:   cb.judgeConfiguration.Clone(),
	}
	return AICompletionConfig{
		aiConfigBase: aiConfigBase{
			enabled: cb.enabled,
			version: defaultVersion(nil),
			model: ModelConfig{
				Name:       cb.modelName,
				Parameters: maps.Clone(cb.modelParams),
				Custom:     maps.Clone(cb.modelCustomParams),
			},
			provider: ProviderConfig{Name: cb.providerName},
		},
		messages:             slices.Clone(cb.messages),
		judgeConfiguration:   cb.judgeConfiguration.Clone(),
		evaluationMetricKey:  cb.evaluationMetricKey,
		evaluationMetricKeys: slices.Clone(cb.evaluationMetricKeys),
		raw:                  raw,
	}
}
