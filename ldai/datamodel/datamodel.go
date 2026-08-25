package datamodel

import (
	"slices"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
)

// Meta defines the serialization format for config metadata.
type Meta struct {
	// VariationKey is the variation key.
	VariationKey string `json:"variationKey,omitempty"`

	// Enabled is true if the config is enabled.
	Enabled bool `json:"enabled,omitempty"`

	// Version is the version of the Variation.
	Version *int `json:"version,omitempty"`

	// Mode is the AI Config mode (e.g., "completion", "agent", "judge").
	// A missing mode defaults to "completion".
	Mode string `json:"mode,omitempty"`

	// ModelKey is the model's stable, unique key (distinct from Model.Name, which is not guaranteed unique).
	ModelKey string `json:"modelKey,omitempty"`

	// ModelVersion is the pinned version of the model that the variation references.
	ModelVersion *int `json:"modelVersion,omitempty"`
}

// Model defines the serialization format for a model.
type Model struct {
	// Name identifies the model.
	Name string `json:"name"`

	// Parameters are the model parameters, generally provided by LaunchDarkly.
	Parameters map[string]ldvalue.Value `json:"parameters,omitempty"`

	// Custom are custom model parameters, generally provided by the user.
	Custom map[string]ldvalue.Value `json:"custom,omitempty"`
}

// Provider defines the serialization format for a model provider.
type Provider struct {
	// Name identifies the provider.
	Name string `json:"name"`
}

// Role defines the role of a message.
type Role string

const (
	// User represents the user.
	User Role = "user"

	// System represents the system.
	System Role = "system"

	// Assistant represents an assistant.
	Assistant Role = "assistant"
)

// Message defines the serialization format for a message which may be passed to an AI model provider.
type Message struct {
	// Content is the message content.
	Content string `json:"content"`

	// Role is the role of the message.
	Role Role `json:"role"`
}

// Tool defines a tool from the root-level "tools" map in the wire format.
// This is distinct from Model.Parameters["tools"] which is the raw LLM-passable array and
// must never be modified by the SDK.
type Tool struct {
	// Name identifies the tool.
	Name string `json:"name"`

	// Description is a human-readable description of the tool.
	Description string `json:"description,omitempty"`

	// Type is the tool type (e.g., "function").
	Type string `json:"type,omitempty"`

	// Parameters are the tool's parameter definitions.
	Parameters map[string]ldvalue.Value `json:"parameters,omitempty"`

	// CustomParameters are custom, user-defined tool parameters.
	CustomParameters map[string]ldvalue.Value `json:"customParameters,omitempty"`
}

// Config defines the serialization format for an AI Config.
type Config struct {
	// Messages is a list of messages. The messages received from LaunchDarkly are uninterpolated.
	Messages []Message `json:"messages,omitempty"`

	// Meta is the config metadata.
	Meta Meta `json:"_ldMeta,omitempty"`

	// Model is the model.
	Model Model `json:"model,omitempty"`

	// Provider is the provider.
	Provider Provider `json:"provider,omitempty"`

	// EvaluationMetricKey is the evaluation metric key for judge mode configs.
	EvaluationMetricKey string `json:"evaluationMetricKey,omitempty"`

	// EvaluationMetricKeys is a deprecated array of evaluation metric keys.
	// Use EvaluationMetricKey instead.
	EvaluationMetricKeys []string `json:"evaluationMetricKeys,omitempty"`

	// JudgeConfiguration specifies judges attached to this config.
	JudgeConfiguration *JudgeConfiguration `json:"judgeConfiguration,omitempty"`

	// Tools is a root-level map of tool name to tool configuration.
	// Distinct from Model.Parameters["tools"] which is the raw LLM-passable array.
	Tools map[string]Tool `json:"tools,omitempty"`

	// Instructions is the agent's system instructions string (agent mode only).
	Instructions string `json:"instructions,omitempty"`
}

// JudgeConfiguration defines the configuration for judges attached to a config.
type JudgeConfiguration struct {
	// Judges is a list of judges to evaluate this config's outputs.
	Judges []Judge `json:"judges,omitempty"`
}

// Clone returns a deep copy of the JudgeConfiguration, or nil if the receiver is nil.
func (j *JudgeConfiguration) Clone() *JudgeConfiguration {
	if j == nil {
		return nil
	}
	return &JudgeConfiguration{Judges: slices.Clone(j.Judges)}
}

// Judge defines a single judge reference with key and sampling rate.
type Judge struct {
	// Key is the judge config key.
	Key string `json:"key"`

	// SamplingRate is the probability (0.0-1.0) that the judge will evaluate.
	SamplingRate float64 `json:"samplingRate"`
}

// EvalScore represents a single evaluation metric result.
type EvalScore struct {
	// Score is the evaluation score between 0.0 and 1.0.
	Score float64 `json:"score"`

	// Reasoning is the explanation for the score.
	Reasoning string `json:"reasoning"`
}

// JudgeResponse represents the response from a judge evaluation.
type JudgeResponse struct {
	// Evals contains the evaluation results keyed by metric name.
	Evals map[string]EvalScore `json:"evals"`

	// Success indicates whether the evaluation completed successfully.
	Success bool `json:"success"`

	// JudgeConfigKey is the key of the judge config that produced this response.
	JudgeConfigKey string `json:"judgeConfigKey,omitempty"`

	// Error contains the error message if the evaluation failed.
	Error string `json:"error,omitempty"`
}
