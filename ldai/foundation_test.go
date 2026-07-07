package ldai

import (
	"encoding/json"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Datamodel round-trip: tools map AND model.parameters.tools[] are distinct
// ---------------------------------------------------------------------------

func TestDatamodelRoundTrip_ToolsMapPreserved(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"tools": {
			"search": {
				"name": "search",
				"description": "Web search",
				"type": "function",
				"parameters": {"q": "hello"},
				"customParameters": {"timeout": 30}
			}
		},
		"model": {
			"name": "my-model",
			"parameters": {
				"tools": [{"type": "function", "function": {"name": "search"}}]
			}
		}
	}`)

	var cfg datamodel.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))

	// Root-level tools map is parsed.
	require.Len(t, cfg.Tools, 1)
	tool := cfg.Tools["search"]
	assert.Equal(t, "search", tool.Name)
	assert.Equal(t, "Web search", tool.Description)
	assert.Equal(t, "function", tool.Type)
	assert.Equal(t, ldvalue.String("hello"), tool.Parameters["q"])
	assert.Equal(t, ldvalue.Int(30), tool.CustomParameters["timeout"])

	// model.parameters.tools[] is preserved verbatim — the SDK must not touch it.
	toolsArray, ok := cfg.Model.Parameters["tools"]
	require.True(t, ok, "model.parameters.tools must be preserved")
	assert.Equal(t, ldvalue.ArrayType, toolsArray.Type())
}

func TestDatamodelRoundTrip_InstructionsField(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"mode": "agent",
		"instructions": "You are a helpful assistant."
	}`)

	var cfg datamodel.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))

	assert.Equal(t, "agent", cfg.Mode)
	assert.Equal(t, "You are a helpful assistant.", cfg.Instructions)
}

// ---------------------------------------------------------------------------
// ToolConfig accessors
// ---------------------------------------------------------------------------

func TestToolConfigAccessors(t *testing.T) {
	wire := datamodel.Tool{
		Name:             "calculator",
		Description:      "Performs arithmetic",
		Type:             "function",
		Parameters:       map[string]ldvalue.Value{"precision": ldvalue.Int(8)},
		CustomParameters: map[string]ldvalue.Value{"internal": ldvalue.Bool(true)},
	}

	tc := toolConfigFromWire(wire)

	assert.Equal(t, "calculator", tc.Name())
	assert.Equal(t, "Performs arithmetic", tc.Description())
	assert.Equal(t, "function", tc.Type())
	assert.Equal(t, ldvalue.Int(8), tc.Parameters()["precision"])
	assert.Equal(t, ldvalue.Bool(true), tc.CustomParameters()["internal"])
}

func TestToolConfig_DefensiveCopyOnParameters(t *testing.T) {
	wire := datamodel.Tool{
		Name:       "tool",
		Parameters: map[string]ldvalue.Value{"key": ldvalue.String("original")},
	}
	tc := toolConfigFromWire(wire)

	// Mutating the returned map must not affect subsequent calls.
	p1 := tc.Parameters()
	p1["key"] = ldvalue.String("mutated")

	p2 := tc.Parameters()
	assert.Equal(t, ldvalue.String("original"), p2["key"])
}

func TestToolConfig_DefensiveCopyOnCustomParameters(t *testing.T) {
	wire := datamodel.Tool{
		Name:             "tool",
		CustomParameters: map[string]ldvalue.Value{"k": ldvalue.Int(1)},
	}
	tc := toolConfigFromWire(wire)

	cp1 := tc.CustomParameters()
	cp1["k"] = ldvalue.Int(99)

	cp2 := tc.CustomParameters()
	assert.Equal(t, ldvalue.Int(1), cp2["k"])
}

// ---------------------------------------------------------------------------
// AICompletionConfig.Tools() – parsed from wire, defensive copy
// ---------------------------------------------------------------------------

func TestCompletionConfig_ParsesToolsMap(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"tools": {
			"search": {"name": "search", "description": "Web search", "type": "function"},
			"calc":   {"name": "calc",   "description": "Calculator"}
		}
	}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	tools := cfg.Tools()
	require.Len(t, tools, 2)
	assert.Equal(t, "search", tools["search"].Name())
	assert.Equal(t, "Web search", tools["search"].Description())
	assert.Equal(t, "function", tools["search"].Type())
	assert.Equal(t, "calc", tools["calc"].Name())
}

func TestCompletionConfig_ToolsDefensiveCopy(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"tools": {"search": {"name": "search"}}
	}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	t1 := cfg.Tools()
	delete(t1, "search")

	t2 := cfg.Tools()
	assert.Contains(t, t2, "search", "deleting from returned map must not affect the config")
}

func TestCompletionConfig_ModelParametersToolsArrayUntouched(t *testing.T) {
	// The LLM-passable tools array in model.parameters.tools must be preserved verbatim.
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"model": {
			"name": "my-model",
			"parameters": {
				"tools": [{"type": "function", "function": {"name": "search"}}]
			}
		}
	}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	toolsParam, ok := cfg.ModelParam("tools")
	require.True(t, ok, "model.parameters.tools must be present")
	assert.Equal(t, ldvalue.ArrayType, toolsParam.Type())
	assert.Equal(t, 1, toolsParam.Count())
}

// ---------------------------------------------------------------------------
// New typed accessors on AICompletionConfig
// ---------------------------------------------------------------------------

func TestCompletionConfig_KeyAccessor(t *testing.T) {
	raw := []byte(`{"_ldMeta": {"variationKey": "v1", "enabled": true}}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("my-flag-key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, "my-flag-key", cfg.Key())
}

func TestCompletionConfig_ModelAndProviderAccessors(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"model": {"name": "my-model", "parameters": {"temperature": 0.7}},
		"provider": {"name": "my-provider"}
	}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	m := cfg.Model()
	assert.Equal(t, "my-model", m.Name)
	assert.Equal(t, ldvalue.Float64(0.7), m.Parameters["temperature"])

	p := cfg.Provider()
	assert.Equal(t, "my-provider", p.Name)
}

func TestCompletionConfig_EvaluatorNeverNil(t *testing.T) {
	client, err := NewClient(newMockSDK([]byte(`{}`), nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.NotNil(t, cfg.Evaluator())
}

func TestCompletionConfig_EvaluatorNeverNilOnManuallyBuiltConfig(t *testing.T) {
	cfg := NewConfig().Enable().Build()
	assert.NotNil(t, cfg.Evaluator())
}

// ---------------------------------------------------------------------------
// Typed defaults
// ---------------------------------------------------------------------------

func TestAICompletionConfigDefault_EnabledByDefault(t *testing.T) {
	d := NewAICompletionConfigDefault()
	v := d.AsLdValue()
	assert.Equal(t, ldvalue.ObjectType, v.Type())

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.True(t, dm.Meta.Enabled)
}

func TestAICompletionConfigDefault_Disabled(t *testing.T) {
	d := NewAICompletionConfigDefault().Disabled()
	v := d.AsLdValue()

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.False(t, dm.Meta.Enabled)
}

func TestAICompletionConfigDefault_AsLdValueRoundTrip(t *testing.T) {
	d := NewAICompletionConfigDefault().
		WithModelName("my-model").
		WithProviderName("my-provider").
		WithMessage("hello", datamodel.User)

	v := d.AsLdValue()
	assert.Equal(t, ldvalue.ObjectType, v.Type())

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.Equal(t, "my-model", dm.Model.Name)
	assert.Equal(t, "my-provider", dm.Provider.Name)
	require.Len(t, dm.Messages, 1)
	assert.Equal(t, "hello", dm.Messages[0].Content)
	assert.Equal(t, datamodel.User, dm.Messages[0].Role)
}

func TestAIAgentConfigDefault_Disabled(t *testing.T) {
	d := NewAIAgentConfigDefault().Disabled()
	v := d.AsLdValue()

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.False(t, dm.Meta.Enabled)
	assert.Equal(t, "agent", dm.Mode)
}

func TestAIAgentConfigDefault_WithInstructions(t *testing.T) {
	d := NewAIAgentConfigDefault().WithInstructions("You are a helpful assistant.")
	v := d.AsLdValue()

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.Equal(t, "You are a helpful assistant.", dm.Instructions)
}

func TestAIJudgeConfigDefault_AsLdValueRoundTrip(t *testing.T) {
	d := NewAIJudgeConfigDefault().
		WithEvaluationMetricKey("toxicity").
		WithMessage("Judge this:", datamodel.System)

	v := d.AsLdValue()

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.Equal(t, "toxicity", dm.EvaluationMetricKey)
	assert.Equal(t, "judge", dm.Mode)
	require.Len(t, dm.Messages, 1)
	assert.Equal(t, "Judge this:", dm.Messages[0].Content)
}

func TestAIJudgeConfigDefault_Disabled(t *testing.T) {
	d := NewAIJudgeConfigDefault().Disabled()
	v := d.AsLdValue()

	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))
	assert.False(t, dm.Meta.Enabled)
}

// ---------------------------------------------------------------------------
// Issue 1: WithModelParam / WithCustomModelParam on agent and judge defaults
// ---------------------------------------------------------------------------

func TestAIAgentConfigDefault_WithModelParam(t *testing.T) {
	d := NewAIAgentConfigDefault().
		WithModelParam("temperature", ldvalue.Float64(0.5)).
		WithCustomModelParam("custom_key", ldvalue.String("val"))

	v := d.AsLdValue()
	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))

	assert.Equal(t, ldvalue.Float64(0.5), dm.Model.Parameters["temperature"])
	assert.Equal(t, ldvalue.String("val"), dm.Model.Custom["custom_key"])
}

func TestAIAgentConfigDefault_WithModelParam_ImmutableCopy(t *testing.T) {
	base := NewAIAgentConfigDefault().WithModelParam("k", ldvalue.Int(1))
	modified := base.WithModelParam("k", ldvalue.Int(2))

	bv := base.AsLdValue()
	var bdm datamodel.Config
	require.NoError(t, json.Unmarshal(bv.AsRaw(), &bdm))
	assert.Equal(t, ldvalue.Int(1), bdm.Model.Parameters["k"], "original must be unchanged")

	mv := modified.AsLdValue()
	var mdm datamodel.Config
	require.NoError(t, json.Unmarshal(mv.AsRaw(), &mdm))
	assert.Equal(t, ldvalue.Int(2), mdm.Model.Parameters["k"])
}

func TestAIJudgeConfigDefault_WithModelParam(t *testing.T) {
	d := NewAIJudgeConfigDefault().
		WithModelParam("temperature", ldvalue.Float64(0.9)).
		WithCustomModelParam("custom_key", ldvalue.String("val"))

	v := d.AsLdValue()
	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))

	assert.Equal(t, ldvalue.Float64(0.9), dm.Model.Parameters["temperature"])
	assert.Equal(t, ldvalue.String("val"), dm.Model.Custom["custom_key"])
}

// ---------------------------------------------------------------------------
// Issue 2: tools + judgeConfiguration on AIAgentConfigDefault
// ---------------------------------------------------------------------------

func TestAIAgentConfigDefault_WithTool(t *testing.T) {
	tool := datamodel.Tool{
		Name:        "search",
		Description: "Web search",
		Type:        "function",
	}
	d := NewAIAgentConfigDefault().WithTool(tool)

	v := d.AsLdValue()
	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))

	require.Contains(t, dm.Tools, "search")
	assert.Equal(t, "search", dm.Tools["search"].Name)
	assert.Equal(t, "Web search", dm.Tools["search"].Description)
	assert.Equal(t, "function", dm.Tools["search"].Type)
}

func TestAIAgentConfigDefault_WithTool_ImmutableCopy(t *testing.T) {
	base := NewAIAgentConfigDefault().WithTool(datamodel.Tool{Name: "a"})
	extended := base.WithTool(datamodel.Tool{Name: "b"})

	bv := base.AsLdValue()
	var bdm datamodel.Config
	require.NoError(t, json.Unmarshal(bv.AsRaw(), &bdm))
	assert.NotContains(t, bdm.Tools, "b", "adding a tool must not mutate the original")

	ev := extended.AsLdValue()
	var edm datamodel.Config
	require.NoError(t, json.Unmarshal(ev.AsRaw(), &edm))
	assert.Contains(t, edm.Tools, "a")
	assert.Contains(t, edm.Tools, "b")
}

func TestAIAgentConfigDefault_WithJudgeConfiguration(t *testing.T) {
	jc := &datamodel.JudgeConfiguration{
		Judges: []datamodel.Judge{
			{Key: "judge1", SamplingRate: 0.5},
		},
	}
	d := NewAIAgentConfigDefault().WithJudgeConfiguration(jc)

	// Mutating the original after calling WithJudgeConfiguration must not affect the default.
	jc.Judges[0].Key = "mutated"

	v := d.AsLdValue()
	var dm datamodel.Config
	require.NoError(t, json.Unmarshal(v.AsRaw(), &dm))

	require.NotNil(t, dm.JudgeConfiguration)
	require.Len(t, dm.JudgeConfiguration.Judges, 1)
	assert.Equal(t, "judge1", dm.JudgeConfiguration.Judges[0].Key)
	assert.Equal(t, 0.5, dm.JudgeConfiguration.Judges[0].SamplingRate)
}

func TestAIJudgeConfigDefault_WithModelParam_ImmutableCopy(t *testing.T) {
	base := NewAIJudgeConfigDefault().WithModelParam("k", ldvalue.Int(1))
	modified := base.WithModelParam("k", ldvalue.Int(2))

	bv := base.AsLdValue()
	var bdm datamodel.Config
	require.NoError(t, json.Unmarshal(bv.AsRaw(), &bdm))
	assert.Equal(t, ldvalue.Int(1), bdm.Model.Parameters["k"], "original must be unchanged")

	mv := modified.AsLdValue()
	var mdm datamodel.Config
	require.NoError(t, json.Unmarshal(mv.AsRaw(), &mdm))
	assert.Equal(t, ldvalue.Int(2), mdm.Model.Parameters["k"])
}

// ---------------------------------------------------------------------------
// Backward compatibility: deprecated Config alias and ConfigBuilder
// ---------------------------------------------------------------------------

func TestConfig_TypeAliasIsAICompletionConfig(t *testing.T) {
	// Config must be an alias for AICompletionConfig — the same type.
	var _ AICompletionConfig = Config{}
	var _ Config = AICompletionConfig{}
}

func TestConfigBuilder_BackwardCompatBuild(t *testing.T) {
	cfg := NewConfig().
		Enable().
		WithModelName("my-model").
		WithProviderName("my-provider").
		WithMessage("hi", datamodel.User).
		WithModelParam("temperature", ldvalue.Float64(0.5)).
		WithCustomModelParam("custom_key", ldvalue.String("val")).
		Build()

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "my-model", cfg.ModelName())
	assert.Equal(t, "my-provider", cfg.ProviderName())

	p, ok := cfg.ModelParam("temperature")
	require.True(t, ok)
	assert.Equal(t, ldvalue.Float64(0.5), p)

	cp, ok := cfg.CustomModelParam("custom_key")
	require.True(t, ok)
	assert.Equal(t, ldvalue.String("val"), cp)

	require.Len(t, cfg.Messages(), 1)
	assert.Equal(t, "hi", cfg.Messages()[0].Content)
}

func TestVersionDefaultsToOne_ForManuallyBuiltConfig(t *testing.T) {
	cfg := NewConfig().Build()
	assert.Equal(t, 1, cfg.Version())
}

func TestVersionFromWire(t *testing.T) {
	raw := []byte(`{"_ldMeta": {"variationKey": "v1", "enabled": true, "version": 7}}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, 7, cfg.Version())
	assert.Equal(t, "v1", cfg.VariationKey())
}
