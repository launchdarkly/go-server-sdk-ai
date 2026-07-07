package ldai

import (
	"maps"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// ToolConfig provides read-only access to a tool definition from the root-level tools map.
// It is distinct from model.parameters.tools which is passed to LLM providers verbatim and
// must never be modified by the SDK (AICONF §1.3.3.1.1).
type ToolConfig struct {
	name             string
	description      string
	toolType         string
	parameters       map[string]ldvalue.Value
	customParameters map[string]ldvalue.Value
}

// Name returns the tool's name.
func (t ToolConfig) Name() string { return t.name }

// Description returns the tool's description.
func (t ToolConfig) Description() string { return t.description }

// Type returns the tool's type (e.g., "function").
func (t ToolConfig) Type() string { return t.toolType }

// Parameters returns a defensive copy of the tool's parameter definitions.
func (t ToolConfig) Parameters() map[string]ldvalue.Value {
	return maps.Clone(t.parameters)
}

// CustomParameters returns a defensive copy of the tool's custom parameters.
func (t ToolConfig) CustomParameters() map[string]ldvalue.Value {
	return maps.Clone(t.customParameters)
}

// toolConfigFromWire converts a datamodel.Tool (wire format) into a ToolConfig (public API).
func toolConfigFromWire(t datamodel.Tool) ToolConfig {
	return ToolConfig{
		name:             t.Name,
		description:      t.Description,
		toolType:         t.Type,
		parameters:       maps.Clone(t.Parameters),
		customParameters: maps.Clone(t.CustomParameters),
	}
}
