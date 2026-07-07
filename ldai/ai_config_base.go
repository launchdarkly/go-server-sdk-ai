package ldai

import (
	"maps"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
)

// ModelConfig holds the model name and associated parameters for an AI Config.
type ModelConfig struct {
	// Name identifies the model (e.g., "gpt-4o").
	Name string

	// Parameters are model parameters generally set by LaunchDarkly.
	Parameters map[string]ldvalue.Value

	// Custom are custom model parameters generally set by the user.
	Custom map[string]ldvalue.Value
}

// ProviderConfig holds the provider name for an AI Config.
type ProviderConfig struct {
	// Name identifies the provider (e.g., "openai").
	Name string
}

// aiConfigBase holds the fields and methods shared across all typed AI configs.
// It is embedded by AICompletionConfig, AIAgentConfig, and AIJudgeConfig.
// It is not exported directly.
type aiConfigBase struct {
	key            string
	enabled        bool
	variationKey   string
	version        int
	model          ModelConfig
	provider       ProviderConfig
	tools          map[string]ToolConfig
	trackerFactory func() *Tracker
	evaluator      *Evaluator
}

// Key returns the feature flag key used to retrieve this config.
func (b *aiConfigBase) Key() string { return b.key }

// Enabled returns whether the config is enabled.
func (b *aiConfigBase) Enabled() bool { return b.enabled }

// Model returns a defensive copy of the model configuration.
func (b *aiConfigBase) Model() ModelConfig {
	return ModelConfig{
		Name:       b.model.Name,
		Parameters: maps.Clone(b.model.Parameters),
		Custom:     maps.Clone(b.model.Custom),
	}
}

// Provider returns the provider configuration.
func (b *aiConfigBase) Provider() ProviderConfig { return b.provider }

// Tools returns a defensive copy of the root-level tools map.
// This is distinct from model.parameters.tools which is passed to LLM providers verbatim.
func (b *aiConfigBase) Tools() map[string]ToolConfig { return maps.Clone(b.tools) }

// CreateTracker returns a new Tracker for a fresh AI run. Each call mints a new runId (a
// UUIDv4) that LaunchDarkly uses to correlate the run's events in metrics views. Call this
// once per AI run; metrics from different runIds cannot be combined.
//
// Returns nil if the config was not obtained via the Client.
func (b *aiConfigBase) CreateTracker() *Tracker {
	if b.trackerFactory == nil {
		return nil
	}
	return b.trackerFactory()
}

// Evaluator returns the evaluator associated with this config. Never returns nil.
func (b *aiConfigBase) Evaluator() *Evaluator { return b.evaluator }
