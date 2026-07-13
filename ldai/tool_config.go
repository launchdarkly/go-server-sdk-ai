package ldai

import (
	"maps"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
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

// toolConfigFromRawValue builds a ToolConfig directly from a raw JSON object value.
// fallbackName is used as the tool name if the entry has no explicit "name" field.
// Used for legacy model.parameters.tools[] entries, which are not parsed via json.Unmarshal.
func toolConfigFromRawValue(item ldvalue.Value, fallbackName string) ToolConfig {
	name := item.GetByKey("name").StringValue()
	if name == "" {
		name = fallbackName
	}

	var parameters map[string]ldvalue.Value
	if pv := item.GetByKey("parameters"); pv.Type() == ldvalue.ObjectType {
		parameters = make(map[string]ldvalue.Value, pv.Count())
		for _, k := range pv.Keys(nil) {
			parameters[k] = pv.GetByKey(k)
		}
	}

	var customParameters map[string]ldvalue.Value
	if cv := item.GetByKey("customParameters"); cv.Type() == ldvalue.ObjectType {
		customParameters = make(map[string]ldvalue.Value, cv.Count())
		for _, k := range cv.Keys(nil) {
			customParameters[k] = cv.GetByKey(k)
		}
	}

	return ToolConfig{
		name:             name,
		description:      item.GetByKey("description").StringValue(),
		toolType:         item.GetByKey("type").StringValue(),
		parameters:       parameters,
		customParameters: customParameters,
	}
}
