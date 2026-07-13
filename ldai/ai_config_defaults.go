package ldai

import (
	"maps"
	"slices"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// AICompletionConfigDefault is the user-constructed default for CompletionConfig calls.
// It is returned when LaunchDarkly is unreachable or the flag cannot be evaluated.
// By default the config is enabled; use Disabled to obtain a disabled default.
type AICompletionConfigDefault struct {
	enabled            bool
	messages           []datamodel.Message
	modelName          string
	modelParams        map[string]ldvalue.Value
	modelCustom        map[string]ldvalue.Value
	providerName       string
	tools              map[string]datamodel.Tool
	judgeConfiguration *datamodel.JudgeConfiguration
}

// NewAICompletionConfigDefault returns a new enabled AICompletionConfigDefault.
func NewAICompletionConfigDefault() AICompletionConfigDefault {
	return AICompletionConfigDefault{
		enabled:     true,
		modelParams: make(map[string]ldvalue.Value),
		modelCustom: make(map[string]ldvalue.Value),
	}
}

// WithEnabled sets whether the default config is enabled.
func (d AICompletionConfigDefault) WithEnabled(enabled bool) AICompletionConfigDefault {
	d.enabled = enabled
	return d
}

// WithMessage appends a message with the given content and role.
func (d AICompletionConfigDefault) WithMessage(content string, role datamodel.Role) AICompletionConfigDefault {
	d.messages = append(slices.Clone(d.messages), datamodel.Message{Content: content, Role: role})
	return d
}

// WithModelName sets the model name.
func (d AICompletionConfigDefault) WithModelName(name string) AICompletionConfigDefault {
	d.modelName = name
	return d
}

// WithProviderName sets the provider name.
func (d AICompletionConfigDefault) WithProviderName(name string) AICompletionConfigDefault {
	d.providerName = name
	return d
}

// WithModelParam sets a model parameter.
func (d AICompletionConfigDefault) WithModelParam(key string, value ldvalue.Value) AICompletionConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelParams)+1)
	maps.Copy(cloned, d.modelParams)
	cloned[key] = value
	d.modelParams = cloned
	return d
}

// WithCustomModelParam sets a custom model parameter.
func (d AICompletionConfigDefault) WithCustomModelParam(key string, value ldvalue.Value) AICompletionConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelCustom)+1)
	maps.Copy(cloned, d.modelCustom)
	cloned[key] = value
	d.modelCustom = cloned
	return d
}

// WithTool adds a tool definition to the default. The tool is keyed by its Name field.
// Call this multiple times to add multiple tools.
func (d AICompletionConfigDefault) WithTool(t datamodel.Tool) AICompletionConfigDefault {
	cloned := make(map[string]datamodel.Tool, len(d.tools)+1)
	maps.Copy(cloned, d.tools)
	cloned[t.Name] = t
	d.tools = cloned
	return d
}

// WithJudgeConfiguration sets the judge configuration. The provided value is defensively copied.
func (d AICompletionConfigDefault) WithJudgeConfiguration(jc *datamodel.JudgeConfiguration) AICompletionConfigDefault {
	d.judgeConfiguration = jc.Clone()
	return d
}

// Disabled returns a copy of this default with enabled set to false.
func (d AICompletionConfigDefault) Disabled() AICompletionConfigDefault {
	d.enabled = false
	return d
}

// AsLdValue serializes this default as an ldvalue.Value for use as a JSONVariation fallback.
func (d AICompletionConfigDefault) AsLdValue() ldvalue.Value {
	return ldvalue.FromJSONMarshal(datamodel.Config{
		Messages: slices.Clone(d.messages),
		Meta:     datamodel.Meta{Enabled: d.enabled},
		Model: datamodel.Model{
			Name:       d.modelName,
			Parameters: maps.Clone(d.modelParams),
			Custom:     maps.Clone(d.modelCustom),
		},
		Provider:           datamodel.Provider{Name: d.providerName},
		Tools:              maps.Clone(d.tools),
		JudgeConfiguration: d.judgeConfiguration.Clone(),
	})
}

// AIAgentConfigDefault is the user-constructed default for AgentConfig calls.
// It is returned when LaunchDarkly is unreachable or the flag cannot be evaluated.
// By default the config is enabled; use Disabled to obtain a disabled default.
type AIAgentConfigDefault struct {
	enabled            bool
	instructions       string
	modelName          string
	modelParams        map[string]ldvalue.Value
	modelCustom        map[string]ldvalue.Value
	providerName       string
	tools              map[string]datamodel.Tool
	judgeConfiguration *datamodel.JudgeConfiguration
}

// NewAIAgentConfigDefault returns a new enabled AIAgentConfigDefault.
func NewAIAgentConfigDefault() AIAgentConfigDefault {
	return AIAgentConfigDefault{
		enabled:     true,
		modelParams: make(map[string]ldvalue.Value),
		modelCustom: make(map[string]ldvalue.Value),
	}
}

// WithEnabled sets whether the default config is enabled.
func (d AIAgentConfigDefault) WithEnabled(enabled bool) AIAgentConfigDefault {
	d.enabled = enabled
	return d
}

// WithInstructions sets the agent's system instructions.
func (d AIAgentConfigDefault) WithInstructions(instructions string) AIAgentConfigDefault {
	d.instructions = instructions
	return d
}

// WithModelName sets the model name.
func (d AIAgentConfigDefault) WithModelName(name string) AIAgentConfigDefault {
	d.modelName = name
	return d
}

// WithProviderName sets the provider name.
func (d AIAgentConfigDefault) WithProviderName(name string) AIAgentConfigDefault {
	d.providerName = name
	return d
}

// WithModelParam sets a model parameter.
func (d AIAgentConfigDefault) WithModelParam(key string, value ldvalue.Value) AIAgentConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelParams)+1)
	maps.Copy(cloned, d.modelParams)
	cloned[key] = value
	d.modelParams = cloned
	return d
}

// WithCustomModelParam sets a custom model parameter.
func (d AIAgentConfigDefault) WithCustomModelParam(key string, value ldvalue.Value) AIAgentConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelCustom)+1)
	maps.Copy(cloned, d.modelCustom)
	cloned[key] = value
	d.modelCustom = cloned
	return d
}

// WithTool adds a tool definition to the default. The tool is keyed by its Name field.
// The provided tool is used as-is; call this multiple times to add multiple tools.
func (d AIAgentConfigDefault) WithTool(t datamodel.Tool) AIAgentConfigDefault {
	cloned := make(map[string]datamodel.Tool, len(d.tools)+1)
	maps.Copy(cloned, d.tools)
	cloned[t.Name] = t
	d.tools = cloned
	return d
}

// WithJudgeConfiguration sets the judge configuration. The provided value is defensively copied.
func (d AIAgentConfigDefault) WithJudgeConfiguration(jc *datamodel.JudgeConfiguration) AIAgentConfigDefault {
	d.judgeConfiguration = jc.Clone()
	return d
}

// Disabled returns a copy of this default with enabled set to false.
func (d AIAgentConfigDefault) Disabled() AIAgentConfigDefault {
	d.enabled = false
	return d
}

// AsLdValue serializes this default as an ldvalue.Value for use as a JSONVariation fallback.
func (d AIAgentConfigDefault) AsLdValue() ldvalue.Value {
	return ldvalue.FromJSONMarshal(datamodel.Config{
		Meta: datamodel.Meta{Enabled: d.enabled, Mode: "agent"},
		Model: datamodel.Model{
			Name:       d.modelName,
			Parameters: maps.Clone(d.modelParams),
			Custom:     maps.Clone(d.modelCustom),
		},
		Provider:           datamodel.Provider{Name: d.providerName},
		Instructions:       d.instructions,
		Tools:              maps.Clone(d.tools),
		JudgeConfiguration: d.judgeConfiguration.Clone(),
	})
}

// AIJudgeConfigDefault is the user-constructed default for JudgeConfig calls.
// It is returned when LaunchDarkly is unreachable or the flag cannot be evaluated.
// By default the config is enabled; use Disabled to obtain a disabled default.
type AIJudgeConfigDefault struct {
	enabled             bool
	messages            []datamodel.Message
	modelName           string
	modelParams         map[string]ldvalue.Value
	modelCustom         map[string]ldvalue.Value
	providerName        string
	evaluationMetricKey string
}

// NewAIJudgeConfigDefault returns a new enabled AIJudgeConfigDefault.
func NewAIJudgeConfigDefault() AIJudgeConfigDefault {
	return AIJudgeConfigDefault{
		enabled:     true,
		modelParams: make(map[string]ldvalue.Value),
		modelCustom: make(map[string]ldvalue.Value),
	}
}

// WithEnabled sets whether the default config is enabled.
func (d AIJudgeConfigDefault) WithEnabled(enabled bool) AIJudgeConfigDefault {
	d.enabled = enabled
	return d
}

// WithMessage appends a message with the given content and role.
func (d AIJudgeConfigDefault) WithMessage(content string, role datamodel.Role) AIJudgeConfigDefault {
	d.messages = append(slices.Clone(d.messages), datamodel.Message{Content: content, Role: role})
	return d
}

// WithModelName sets the model name.
func (d AIJudgeConfigDefault) WithModelName(name string) AIJudgeConfigDefault {
	d.modelName = name
	return d
}

// WithProviderName sets the provider name.
func (d AIJudgeConfigDefault) WithProviderName(name string) AIJudgeConfigDefault {
	d.providerName = name
	return d
}

// WithEvaluationMetricKey sets the evaluation metric key.
func (d AIJudgeConfigDefault) WithEvaluationMetricKey(key string) AIJudgeConfigDefault {
	d.evaluationMetricKey = key
	return d
}

// WithModelParam sets a model parameter.
func (d AIJudgeConfigDefault) WithModelParam(key string, value ldvalue.Value) AIJudgeConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelParams)+1)
	maps.Copy(cloned, d.modelParams)
	cloned[key] = value
	d.modelParams = cloned
	return d
}

// WithCustomModelParam sets a custom model parameter.
func (d AIJudgeConfigDefault) WithCustomModelParam(key string, value ldvalue.Value) AIJudgeConfigDefault {
	cloned := make(map[string]ldvalue.Value, len(d.modelCustom)+1)
	maps.Copy(cloned, d.modelCustom)
	cloned[key] = value
	d.modelCustom = cloned
	return d
}

// Disabled returns a copy of this default with enabled set to false.
func (d AIJudgeConfigDefault) Disabled() AIJudgeConfigDefault {
	d.enabled = false
	return d
}

// AsLdValue serializes this default as an ldvalue.Value for use as a JSONVariation fallback.
func (d AIJudgeConfigDefault) AsLdValue() ldvalue.Value {
	return ldvalue.FromJSONMarshal(datamodel.Config{
		Messages: slices.Clone(d.messages),
		Meta:     datamodel.Meta{Enabled: d.enabled, Mode: "judge"},
		Model: datamodel.Model{
			Name:       d.modelName,
			Parameters: maps.Clone(d.modelParams),
			Custom:     maps.Clone(d.modelCustom),
		},
		Provider:            datamodel.Provider{Name: d.providerName},
		EvaluationMetricKey: d.evaluationMetricKey,
	})
}
