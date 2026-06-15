package ldai

import (
	"context"
	"errors"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldlog"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentConfig_Basic(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "version": 3, "mode": "agent"},
		"mode": "agent",
		"model": {"name": "gpt-4"},
		"provider": {"name": "openai"},
		"instructions": "You are an agent helping {{ldctx.name}} with {{task}}.",
		"tools": {
			"get_weather": {"description": "Looks up the weather", "type": "function"}
		}
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil // discard the SDK info event

	ldctx := ldcontext.NewBuilder("user-key").Name("Sandy").Build()
	cfg := client.AgentConfig(context.Background(), "my-agent", ldctx, Disabled(),
		map[string]interface{}{"task": "the weather"})

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "You are an agent helping Sandy with the weather.", cfg.Instructions())
	assert.Equal(t, "gpt-4", cfg.ModelName())
	assert.Equal(t, "v1", cfg.VariationKey())
	assert.Equal(t, 3, cfg.Version())

	tools := cfg.Tools()
	require.Contains(t, tools, "get_weather")
	assert.Equal(t, "get_weather", tools["get_weather"].Name, "tool name defaults to the map key")
	assert.Equal(t, "Looks up the weather", tools["get_weather"].Description)

	require.NotNil(t, cfg.CreateTracker())

	// The usage event is emitted with the config key.
	require.Len(t, mockSDK.events, 1)
	evt := mockSDK.events[0]
	assert.Equal(t, "$ld:ai:usage:agent-config", evt.eventName)
	assert.Equal(t, float64(1), evt.metricValue)
	assert.Equal(t, "my-agent", evt.data.GetByKey("configKey").StringValue())
}

func TestAgentConfig_ModeMismatchReturnsDisabled(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "version": 2, "mode": "completion"},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), Disabled(), nil)

	assert.False(t, cfg.Enabled())
	assert.Empty(t, cfg.Messages(), "mode-mismatched config content must not leak through")
	assert.Equal(t, modeAgent, cfg.Mode())
	assert.Equal(t, "v1", cfg.VariationKey(), "metadata is preserved for tracking")
	assert.NotNil(t, cfg.CreateTracker())
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "mode mismatch")
}

func TestAgentConfig_TopLevelModeFallback(t *testing.T) {
	// Mode only at the top level (no _ldMeta.mode): still detected as a mismatch.
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"mode": "judge"
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), Disabled(), nil)
	assert.False(t, cfg.Enabled())
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "mode mismatch")
}

func TestAgentConfig_UnspecifiedModeIsAccepted(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true},
		"instructions": "You are an agent."
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), Disabled(), nil)
	assert.True(t, cfg.Enabled())
	assert.Equal(t, "You are an agent.", cfg.Instructions())
}

func TestAgentConfig_EvalErrorReturnsDefault(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, errors.New("offline")))
	require.NoError(t, err)

	// The default is interpolated like a served config (matching the Python SDK): {{task}} is
	// rendered from the supplied variables even on the evaluation-failure path.
	defaultVal := NewConfig().Enable().WithInstructions("Default instructions for {{task}}.").Build()
	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), defaultVal,
		map[string]interface{}{"task": "research"})

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "Default instructions for research.", cfg.Instructions())
	assert.NotNil(t, cfg.CreateTracker())
}

func TestAgentConfig_MalformedInstructionsReturnsDefault(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "mode": "agent"},
		"instructions": "Broken {{#section} instructions"
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	defaultVal := NewConfig().Enable().WithInstructions("fallback").Build()
	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), defaultVal, nil)

	assert.Equal(t, "fallback", cfg.Instructions())
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "malformed instructions")
}

func TestAgentConfig_CanceledContextReturnsDefault(t *testing.T) {
	json := []byte(`{"_ldMeta": {"enabled": true, "mode": "agent"}, "instructions": "live"}`)
	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defaultVal := NewConfig().Enable().WithInstructions("fallback").Build()
	cfg := client.AgentConfig(ctx, "key", ldcontext.New("user"), defaultVal, nil)

	assert.Equal(t, "fallback", cfg.Instructions())
	assert.NotNil(t, cfg.CreateTracker())
}

func TestAgentConfigs_Batch(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "mode": "agent"},
		"instructions": "You handle {{task}}."
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	agents := client.AgentConfigs(context.Background(), []AgentConfigRequest{
		{Key: "research-agent", DefaultValue: Disabled(), Variables: map[string]interface{}{"task": "research"}},
		{Key: "writing-agent", DefaultValue: Disabled(), Variables: map[string]interface{}{"task": "writing"}},
	}, ldcontext.New("user"))

	require.Len(t, agents, 2)
	research, writing := agents["research-agent"], agents["writing-agent"]
	assert.Equal(t, "You handle research.", research.Instructions(),
		"each request is interpolated with its own variables")
	assert.Equal(t, "You handle writing.", writing.Instructions())

	// One batch usage event; no per-key usage events.
	require.Len(t, mockSDK.events, 1)
	evt := mockSDK.events[0]
	assert.Equal(t, "$ld:ai:usage:agent-configs", evt.eventName)
	assert.Equal(t, float64(2), evt.metricValue)
	assert.Equal(t, 2, evt.data.GetByKey("count").IntValue())
}

func TestAgentConfigs_CanceledContextReturnsDefaults(t *testing.T) {
	json := []byte(`{"_ldMeta": {"enabled": true, "mode": "agent"}, "instructions": "live"}`)
	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	defaultVal := NewConfig().Enable().WithInstructions("fallback").Build()
	agents := client.AgentConfigs(ctx, []AgentConfigRequest{
		{Key: "a", DefaultValue: defaultVal},
		{Key: "b", DefaultValue: defaultVal},
	}, ldcontext.New("user"))

	a, b := agents["a"], agents["b"]
	assert.Equal(t, "fallback", a.Instructions())
	assert.Equal(t, "fallback", b.Instructions())
}

func TestToolsParsing_TopLevelNamePrecedence(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"enabled": true},
		"tools": {
			"alias": {"name": "real_name", "description": "explicit name wins"},
			"unnamed": {"description": "name defaults to key"}
		}
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	// Tools are parsed for every config kind (parity with Python).
	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	tools := cfg.Tools()
	require.Len(t, tools, 2)
	assert.Equal(t, "real_name", tools["alias"].Name)
	assert.Equal(t, "unnamed", tools["unnamed"].Name)
}

func TestToolsParsing_ModelParametersFallback(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"enabled": true},
		"model": {
			"name": "gpt-4",
			"parameters": {
				"tools": [
					{"name": "search", "description": "Searches", "type": "function",
					 "parameters": {"type": "object"}},
					{"description": "missing name, skipped"},
					"not an object"
				]
			}
		}
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	tools := cfg.Tools()
	require.Len(t, tools, 1)
	assert.Equal(t, "search", tools["search"].Name)
	assert.Equal(t, "Searches", tools["search"].Description)
	assert.Equal(t, "function", tools["search"].Type)
	assert.Equal(t, ldvalue.ObjectBuild().Set("type", ldvalue.String("object")).Build(),
		tools["search"].Parameters)
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "missing name")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "expected an object")
}

func TestToolsParsing_MalformedTopLevelEntrySkippedNotFatal(t *testing.T) {
	// A non-object entry in the top-level tools map is skipped with a warning; the rest of the
	// config (and the well-formed tools) is still served. Matches the Python SDK's tolerance.
	json := []byte(`{
		"_ldMeta": {"enabled": true},
		"instructions": "still here",
		"tools": {
			"good": {"description": "fine"},
			"bad": "not an object"
		}
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	assert.True(t, cfg.Enabled(), "one malformed tool must not discard the whole config")
	assert.Equal(t, "still here", cfg.Instructions())
	tools := cfg.Tools()
	require.Len(t, tools, 1)
	assert.Contains(t, tools, "good")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, `skipping tool "bad"`)
}

func TestParsing_MalformedFieldDoesNotDiscardConfig(t *testing.T) {
	// A structurally malformed field is skipped, not fatal: the rest of the config is still served
	// (matching the Python SDK's field-by-field parsing).
	cases := map[string]string{
		"messages not an array":     `{"_ldMeta":{"enabled":true},"model":{"name":"m"},"messages":"oops"}`,
		"message entry not object":  `{"_ldMeta":{"enabled":true},"model":{"name":"m"},"messages":[{"content":"hi","role":"user"},"oops"]}`,
		"tools wrong field type":    `{"_ldMeta":{"enabled":true},"model":{"name":"m"},"tools":"oops"}`,
		"metric keys with non-str":  `{"_ldMeta":{"enabled":true},"model":{"name":"m"},"evaluationMetricKeys":["good",7]}`,
		"judge entry missing field": `{"_ldMeta":{"enabled":true},"model":{"name":"m"},"judgeConfiguration":{"judges":[{"key":"j"}]}}`,
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			client, err := NewClient(newMockSDK([]byte(payload), nil))
			require.NoError(t, err)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

			assert.True(t, cfg.Enabled(), "the malformed field must not discard the whole config")
			assert.Equal(t, "m", cfg.ModelName(), "well-formed fields are still served")
		})
	}
}

func TestParsing_MalformedMessageEntryDropsAllMessages(t *testing.T) {
	// Python drops the entire messages list when any entry is not an object, but keeps the config.
	json := []byte(`{"_ldMeta":{"enabled":true},"messages":[{"content":"hi","role":"user"},42]}`)
	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.True(t, cfg.Enabled())
	assert.Empty(t, cfg.Messages())
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "skipping messages")
}

func TestParsing_MetricKeysAndJudgesSkipBadEntries(t *testing.T) {
	json := []byte(`{
		"_ldMeta":{"enabled":true},
		"evaluationMetricKeys":["a", 7, "b"],
		"judgeConfiguration":{"judges":[
			{"key":"good","samplingRate":0.5},
			{"key":"missing-rate"},
			"not-an-object"
		]}
	}`)
	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, []string{"a", "b"}, cfg.EvaluationMetricKeys())

	jc := cfg.JudgeConfiguration()
	require.NotNil(t, jc)
	require.Len(t, jc.Judges, 1)
	assert.Equal(t, "good", jc.Judges[0].Key)
	assert.Equal(t, 0.5, jc.Judges[0].SamplingRate)
}

func TestParsing_JudgeEntriesWithWrongFieldTypesSkipped(t *testing.T) {
	// A judge whose key is not a string or samplingRate is not a number is skipped, not coerced to
	// "" / 0.0 (which would silently mean "never sample").
	json := []byte(`{
		"_ldMeta":{"enabled":true},
		"judgeConfiguration":{"judges":[
			{"key":"ok","samplingRate":1},
			{"key":7,"samplingRate":0.5},
			{"key":"bad-rate","samplingRate":"high"}
		]}
	}`)
	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	jc := cfg.JudgeConfiguration()
	require.NotNil(t, jc)
	require.Len(t, jc.Judges, 1)
	assert.Equal(t, "ok", jc.Judges[0].Key)
}

func TestToolsParsing_TopLevelTakesPrecedenceOverModelParameters(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"enabled": true},
		"tools": {"primary": {"description": "from top level"}},
		"model": {"name": "m", "parameters": {"tools": [{"name": "fallback"}]}}
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	tools := cfg.Tools()
	require.Len(t, tools, 1)
	assert.Contains(t, tools, "primary")
}

func TestConfig_ToolsAreCopied(t *testing.T) {
	cfg := NewConfig().
		WithTools(map[string]datamodel.Tool{"search": {Name: "search"}}).
		Build()

	tools := cfg.Tools()
	tools["injected"] = datamodel.Tool{Name: "injected"}
	tools["search"] = datamodel.Tool{Name: "mutated"}

	fresh := cfg.Tools()
	require.Len(t, fresh, 1)
	assert.Equal(t, "search", fresh["search"].Name)
}

func TestConfigBuilder_WithToolsIsDefensive(t *testing.T) {
	original := map[string]datamodel.Tool{"search": {Name: "search"}}
	builder := NewConfig().WithTools(original)
	original["late"] = datamodel.Tool{Name: "late addition"}

	cfg := builder.Build()
	require.Len(t, cfg.Tools(), 1)
}

func TestConfigBuilder_WithInstructions(t *testing.T) {
	cfg := NewConfig().WithInstructions("You are an agent.").Build()
	assert.Equal(t, "You are an agent.", cfg.Instructions())
}

func TestAgentConfig_DefaultWithNonAgentModeIsNotValidated(t *testing.T) {
	// On evaluation failure the caller's default is returned verbatim — mode validation must not
	// fire on it, even if the default carries a non-agent mode (e.g. a reused completion config).
	client, err := NewClient(newMockSDK(nil, errors.New("offline")))
	require.NoError(t, err)

	defaultVal := NewConfig().Enable().WithMode("completion").WithInstructions("fallback").Build()
	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), defaultVal, nil)

	assert.True(t, cfg.Enabled(), "the default must be returned as-is, not replaced by a disabled config")
	assert.Equal(t, "completion", cfg.Mode())
	assert.Equal(t, "fallback", cfg.Instructions())
}

func TestAgentConfig_ModeMismatchDoesNotLeakViaTracker(t *testing.T) {
	// A mode-mismatched config is returned disabled; the tracker it mints must also see the disabled
	// config in its task callback, not the original (enabled, message-bearing) served config.
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "version": 7, "mode": "completion"},
		"messages": [{"content": "secret", "role": "user"}],
		"model": {"name": "gpt-4"}
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), Disabled(), nil)
	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker)

	var sawEnabled bool
	var sawMessages int
	var sawModel string
	_, _ = tracker.TrackRequest(func(c *Config) (ProviderResponse, error) {
		sawEnabled = c.Enabled()
		sawMessages = len(c.Messages())
		sawModel = c.ModelName()
		return ProviderResponse{}, nil
	})

	assert.False(t, sawEnabled, "tracker callback must not see the original enabled config")
	assert.Zero(t, sawMessages, "tracker callback must not see the rejected config's messages")
	assert.Empty(t, sawModel, "tracker callback must not see the rejected config's model")
}

func TestAgentConfig_ModeReportedFromMetadata(t *testing.T) {
	// A served agent config carries its mode only in _ldMeta; Mode() must surface it.
	json := []byte(`{
		"_ldMeta": {"variationKey": "v1", "enabled": true, "mode": "agent"},
		"instructions": "hi"
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig(context.Background(), "key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, modeAgent, cfg.Mode())
}

func TestToolsParsing_EmptyTopLevelToolsSuppressesFallback(t *testing.T) {
	// A present-but-empty top-level tools map is authoritative: the legacy model.parameters.tools
	// array must NOT resurrect deleted tools (parity with Python's _resolve_tools).
	json := []byte(`{
		"_ldMeta": {"enabled": true},
		"tools": {},
		"model": {"name": "m", "parameters": {"tools": [{"name": "stale"}]}}
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Empty(t, cfg.Tools(), "empty top-level tools must not fall back to model parameters")
}
