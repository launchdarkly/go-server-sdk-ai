package ldai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldlog"
	"github.com/launchdarkly/go-sdk-common/v4/ldlogtest"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

type mockServerSDK struct {
	log    *ldlogtest.MockLog
	json   []byte
	err    error
	events []mockEvent
}

type mockEvent struct {
	eventName   string
	context     ldcontext.Context
	metricValue float64
	data        ldvalue.Value
}

func newMockSDK(json []byte, err error) *mockServerSDK {
	return &mockServerSDK{json: json, err: err, log: ldlogtest.NewMockLog(), events: []mockEvent{}}
}

func (m *mockServerSDK) JSONVariation(
	key string,
	context ldcontext.Context,
	defaultVal ldvalue.Value,
) (ldvalue.Value, error) {

	if m.err != nil {
		return defaultVal, m.err
	}

	return ldvalue.Parse(m.json), nil
}

func (m *mockServerSDK) Loggers() interfaces.LDLoggers {
	return m.log.Loggers
}

func (m *mockServerSDK) TrackMetric(eventName string, context ldcontext.Context, metricValue float64, data ldvalue.Value) error {
	m.events = append(m.events, mockEvent{
		eventName:   eventName,
		context:     context,
		metricValue: metricValue,
		data:        data,
	})
	return nil
}

func TestNewClientReturnsErrorWhenSDKIsNil(t *testing.T) {
	_, err := NewClient(nil)
	require.Error(t, err)
}

func TestNewClient(t *testing.T) {
	mockSDK := newMockSDK(nil, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify SDK info event was fired on construction.
	require.Len(t, mockSDK.events, 1)
	evt := mockSDK.events[0]
	assert.Equal(t, "$ld:ai:sdk:info", evt.eventName)
	assert.Equal(t, float64(1), evt.metricValue)
	assert.Equal(t, "go-server-sdk-ai", evt.data.GetByKey("aiSdkName").StringValue())
	assert.Equal(t, Version, evt.data.GetByKey("aiSdkVersion").StringValue())
	assert.Equal(t, "go", evt.data.GetByKey("aiSdkLanguage").StringValue())

	// Verify the context is anonymous with kind ld_ai.
	assert.Equal(t, "ld-internal-tracking", evt.context.Key())
	assert.True(t, evt.context.Anonymous())
	assert.Equal(t, ldcontext.Kind("ld_ai"), evt.context.Kind())
}

func TestEvalErrorReturnsDefault(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, errors.New("client is offline")))
	require.NoError(t, err)
	require.NotNil(t, client)

	// The literal {{x}} must survive: interpolation is not applied to the default value's messages.
	def := NewAICompletionConfigDefault().WithMessage("hello {{x}}", datamodel.User)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)
	assert.NotNil(t, cfg.CreateTracker())
	assert.True(t, cfg.Enabled())
	assert.Equal(t, []datamodel.Message{{Content: "hello {{x}}", Role: datamodel.User}}, cfg.Messages())
	assert.Equal(t, "", cfg.ModelName())
	assert.Equal(t, "", cfg.ProviderName())
}

func TestInterpolationDoesNotHTMLEscape(t *testing.T) {
	vars := map[string]interface{}{"name": "Tom & Jerry's <best> show"}

	t.Run("double mustache", func(t *testing.T) {
		result, err := eval(t, "Hello {{name}}", ldcontext.New("user"), vars)
		require.NoError(t, err)
		assert.Equal(t, "Hello Tom & Jerry's <best> show", result)
	})

	t.Run("triple mustache still works", func(t *testing.T) {
		result, err := eval(t, "Hello {{{name}}}", ldcontext.New("user"), vars)
		require.NoError(t, err)
		assert.Equal(t, "Hello Tom & Jerry's <best> show", result)
	})

	t.Run("ampersand tag still works", func(t *testing.T) {
		result, err := eval(t, "Hello {{& name}}", ldcontext.New("user"), vars)
		require.NoError(t, err)
		assert.Equal(t, "Hello Tom & Jerry's <best> show", result)
	})

	t.Run("context attributes are not escaped", func(t *testing.T) {
		ctx := ldcontext.NewBuilder("user").SetString("company", "Smith & Sons").Build()
		result, err := eval(t, "Works at {{ldctx.company}}", ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, "Works at Smith & Sons", result)
	})
}

func TestConfigExposesVariationKeyAndVersion(t *testing.T) {
	raw := []byte(`{"_ldMeta": {"variationKey": "var-1", "enabled": true, "version": 5}}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, "var-1", cfg.VariationKey())
	assert.Equal(t, 5, cfg.Version())
}

func TestExplicitVersionZeroInTracker(t *testing.T) {
	// Version 0 from the wire must be preserved in the tracker's event data.
	raw := []byte(`{"_ldMeta": {"variationKey": "var-1", "enabled": true, "version": 0}}`)

	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	require.Equal(t, 0, cfg.Version())

	events := newMockEvents()
	tracker := newTracker(events, newRunID(), "key", cfg.VariationKey(), cfg.Version(), ldcontext.New("user"), &cfg, nil, "")
	_ = tracker.TrackSuccess()

	require.Len(t, events.events, 1)
	assert.Equal(t, 0, events.events[0].data.GetByKey("version").IntValue())
}

func TestVersion_AbsentDefaultsToOne(t *testing.T) {
	// Wire config with no version field must report Version() == 1.
	raw := []byte(`{"_ldMeta": {"variationKey": "v1", "enabled": true}}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)
	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, 1, cfg.Version())
}

func TestVersion_ExplicitNonZero(t *testing.T) {
	// Wire config with an explicit version must be returned verbatim.
	raw := []byte(`{"_ldMeta": {"variationKey": "v1", "enabled": true, "version": 7}}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)
	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, 7, cfg.Version())
}

func TestVersion_DefaultPathReturnsOne(t *testing.T) {
	// Error/default path must report Version() == 1 and have a working CreateTracker.
	mockSDK := newMockSDK(nil, fmt.Errorf("offline"))
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, 1, cfg.Version())
	assert.NotNil(t, cfg.CreateTracker())
}

func TestParseMultipleMessages(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"messages": [
			{"content": "hello", "role": "user"},
			{"content": "world", "role": "system"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	assert.ElementsMatch(t, cfg.Messages(), []datamodel.Message{
		{Content: "hello", Role: datamodel.User},
		{Content: "world", Role: datamodel.System},
	})
}

func TestParseModelName(t *testing.T) {
	tests := []struct {
		name     string
		json     []byte
		expected string
	}{
		{"missing", []byte(`{"model": {}}`), ""},
		{"empty string", []byte(`{"model": {"name": ""}}`), ""},
		{"non-empty string", []byte(`{"model": {"name": "my-model"}}`), "my-model"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(newMockSDK(test.json, nil))
			require.NoError(t, err)
			require.NotNil(t, client)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), NewAICompletionConfigDefault(), nil)

			assert.Equal(t, test.expected, cfg.ModelName())
		})
	}
}

func TestParseProviderName(t *testing.T) {
	tests := []struct {
		name     string
		json     []byte
		expected string
	}{
		{"missing", []byte(`{"provider": {}}`), ""},
		{"empty string", []byte(`{"provider": {"name": ""}}`), ""},
		{"non-empty string", []byte(`{"provider": {"name": "my-provider"}}`), "my-provider"}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(newMockSDK(test.json, nil))
			require.NoError(t, err)
			require.NotNil(t, client)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), NewAICompletionConfigDefault(), nil)

			assert.Equal(t, test.expected, cfg.ProviderName())
		})
	}
}

func TestParseInvalidConfigReturnsDefault(t *testing.T) {
	tests := []struct {
		name string
		json []byte
	}{
		{"null value", []byte("null")},
		{"invalid json", []byte("invalid")},
		{"is a number", []byte("42")},
		{"is a string", []byte(`"hello"`)},
		{"is an array", []byte(`["hello"]`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sdk := newMockSDK(test.json, nil)
			client, err := NewClient(sdk)
			require.NoError(t, err)
			require.NotNil(t, client)

			def := NewAICompletionConfigDefault().WithMessage("hello", datamodel.User)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)
			// Verify config data matches the default
			assert.Equal(t, def.AsLdValue(), cfg.AsLdValue())
			// Verify CreateTracker() now works (returnDefault always injects a factory)
			assert.NotNil(t, cfg.CreateTracker())

			sdk.log.AssertMessageMatch(t, true, ldlog.Warn, "AI Config 'key':")
		})
	}
}

func TestParseDisabledConfigs(t *testing.T) {
	tests := []struct {
		name string
		json []byte
	}{
		{"empty object", []byte("{}")},
		{"missing meta field", []byte(`{"model": {}, "messages": []}`)},
		{"meta disabled explicitly", []byte(`{"meta": {"enabled": false, "variationKey": "1"}, "model": {}, "messages": []}`)},
		{"meta disable implicitly", []byte(`{"meta": { "variationKey": "1"}, "model": {}, "messages": []}`)},
	}

	def := NewAICompletionConfigDefault().WithMessage("hello", datamodel.User)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(newMockSDK(test.json, nil))
			require.NoError(t, err)
			require.NotNil(t, client)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

			// We *shouldn't* be getting the default value, because these are all valid configs that should
			// be parsed as disabled.
			assert.False(t, cfg.Enabled())
		})
	}
}

func TestParseModelParams(t *testing.T) {
	tests := []struct {
		name     string
		json     []byte
		expected map[string]ldvalue.Value
	}{
		{"omitted", []byte(`{"model": {"name": "model"}}`), nil},
		{"empty", []byte(`{"model": {"name": "model", "parameters": {}}}`), map[string]ldvalue.Value{}},
		{"single", []byte(`{"model": {"name": "model", "parameters": {"foo": "bar"}}}`),
			map[string]ldvalue.Value{"foo": ldvalue.String("bar")}},
		{"multiple", []byte(`{"model": {"name": "model", "parameters": {"foo": "bar", "baz": 42}}}`),
			map[string]ldvalue.Value{"foo": ldvalue.String("bar"), "baz": ldvalue.Int(42)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(newMockSDK(test.json, nil))
			require.NoError(t, err)
			require.NotNil(t, client)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), NewAICompletionConfigDefault(), nil)

			for k, v := range test.expected {
				p, ok := cfg.ModelParam(k)
				if assert.True(t, ok) {
					assert.Equal(t, v, p)
				}
			}
		})
	}
}

func TestParseCustomModelParams(t *testing.T) {
	tests := []struct {
		name     string
		json     []byte
		expected map[string]ldvalue.Value
	}{
		{"omitted", []byte(`{"model": {"name": "model"}}`), nil},
		{"empty", []byte(`{"model": {"name": "model", "custom": {}}}`), map[string]ldvalue.Value{}},
		{"single", []byte(`{"model": {"name": "model", "custom": {"foo": "bar"}}}`),
			map[string]ldvalue.Value{"foo": ldvalue.String("bar")}},
		{"multiple", []byte(`{"model": {"name": "model", "custom": {"foo": "bar", "baz": 42}}}`),
			map[string]ldvalue.Value{"foo": ldvalue.String("bar"), "baz": ldvalue.Int(42)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(newMockSDK(test.json, nil))
			require.NoError(t, err)
			require.NotNil(t, client)

			cfg := client.CompletionConfig("key", ldcontext.New("user"), NewAICompletionConfigDefault(), nil)

			for k, v := range test.expected {
				p, ok := cfg.CustomModelParam(k)
				if assert.True(t, ok) {
					assert.Equal(t, v, p)
				}
			}
		})
	}
}

func TestCanSetDefaultConfigFields(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	def := NewAICompletionConfigDefault().
		WithMessage("hello", datamodel.User).
		WithMessage("world", datamodel.System).
		WithProviderName("provider").
		WithModelName("model")

	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "provider", cfg.ProviderName())
	assert.Equal(t, "model", cfg.ModelName())
	assert.Equal(t, 2, len(cfg.Messages()))

	msg := cfg.Messages()
	assert.Equal(t, "hello", msg[0].Content)
	assert.Equal(t, datamodel.User, msg[0].Role)
	assert.Equal(t, "world", msg[1].Content)
	assert.Equal(t, datamodel.System, msg[1].Role)
}

func TestCompletionConfigMethodTracking(t *testing.T) {
	mockSDK := newMockSDK(nil, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Clear the SDK info event from construction.
	mockSDK.events = nil

	defaultConfig := NewAICompletionConfigDefault().Disabled()
	context := ldcontext.New("user-key")
	configKey := "test-config-key"

	config := client.CompletionConfig(configKey, context, defaultConfig, nil)

	require.NotNil(t, config.CreateTracker())

	expectedData := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(configKey)).Build()
	expectedEvents := []mockEvent{
		{
			eventName:   "$ld:ai:usage:completion-config",
			context:     context,
			metricValue: 1,
			data:        expectedData,
		},
	}

	assert.ElementsMatch(t, expectedEvents, mockSDK.events)
}

// TestJudgeConfigMethodTracking verifies that JudgeConfig emits only the judge metric,
// not the completion-config metric, so judge evaluations are not double-counted on the dashboard.
func TestJudgeConfigMethodTracking(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"messages": [{"content": "test", "role": "system"}]
	}`)
	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Clear the SDK info event from construction.
	mockSDK.events = nil

	defaultConfig := NewAIJudgeConfigDefault()
	context := ldcontext.New("user-key")
	configKey := "judge-config-key"

	config := client.JudgeConfig(configKey, context, defaultConfig, nil)

	require.NotNil(t, config.CreateTracker())

	// Only the judge metric should be emitted; evaluateConfig does not emit any metric.
	expectedData := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(configKey)).Build()
	expectedEvents := []mockEvent{
		{
			eventName:   "$ld:ai:usage:judge-config",
			context:     context,
			metricValue: 1,
			data:        expectedData,
		},
	}
	assert.ElementsMatch(t, expectedEvents, mockSDK.events,
		"JudgeConfig must not emit $ld:ai:usage:completion-config to avoid double-counting")
}

func TestCanSetModelParameters(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	def := NewAICompletionConfigDefault().WithModelParam("foo", ldvalue.String("bar"))
	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

	t.Run("param is present", func(t *testing.T) {
		p, ok := cfg.ModelParam("foo")
		assert.True(t, ok)
		assert.Equal(t, "bar", p.StringValue())
	})

	t.Run("param is missing", func(t *testing.T) {
		p, ok := cfg.ModelParam("missing")
		assert.False(t, ok)
		assert.Equal(t, ldvalue.Null(), p)
	})
}

func TestCanSetCustomModelParameters(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	def := NewAICompletionConfigDefault().WithCustomModelParam("foo", ldvalue.String("bar"))
	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

	t.Run("param is present", func(t *testing.T) {
		p, ok := cfg.CustomModelParam("foo")
		assert.True(t, ok)
		assert.Equal(t, "bar", p.StringValue())
	})

	t.Run("param is missing", func(t *testing.T) {
		p, ok := cfg.CustomModelParam("missing")
		assert.False(t, ok)
		assert.Equal(t, ldvalue.Null(), p)
	})
}

func TestNormalAndCustomParamsDoNotInterfere(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	def := NewAICompletionConfigDefault().
		WithModelParam("foo", ldvalue.String("bar")).
		WithCustomModelParam("foo", ldvalue.String("baz"))

	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

	foo1, ok := cfg.ModelParam("foo")
	require.True(t, ok)
	assert.Equal(t, "bar", foo1.StringValue())

	foo2, ok := cfg.CustomModelParam("foo")
	require.True(t, ok)
	assert.Equal(t, "baz", foo2.StringValue())
}

func TestCannotOverwriteMessages(t *testing.T) {
	client, err := NewClient(newMockSDK(nil, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	def := NewAICompletionConfigDefault().WithMessage("hello", datamodel.Assistant)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), def, nil)

	cfg.Messages()[0].Content = "changed"
	cfg.Messages()[0].Role = datamodel.User

	assert.ElementsMatch(t, []datamodel.Message{{Content: "hello", Role: datamodel.Assistant}}, cfg.Messages())
}

func eval(t *testing.T, prompt string, ctx ldcontext.Context, variables map[string]interface{}) (string, error) {
	t.Helper()
	json := []byte(`{
					"_ldMeta": {"variationKey": "1", "enabled": true},
					"messages": [
						{"content": "` + prompt + `", "role": "user"}
					]
				}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	cfg := client.CompletionConfig("key", ctx, Disabled(), variables)
	if len(cfg.Messages()) == 0 {
		return "", errors.New("no messages interpolated")
	}
	return cfg.Messages()[0].Content, nil
}

func TestInterpolation(t *testing.T) {
	t.Run("missing variables", func(t *testing.T) {
		cases := []string{
			"{{ adjective }}",
			"{{ adjective.nested.deep }}",
			"{{ ldctx.this_is_not_a_variable }}",
		}

		for _, c := range cases {
			t.Run(c, func(t *testing.T) {
				result, err := eval(t, "I am an ("+c+") LLM", ldcontext.New("user"), nil)
				require.NoError(t, err)
				assert.Equal(t, "I am an () LLM", result)
			})
		}
	})

	t.Run("simple variables", func(t *testing.T) {
		cases := []string{
			"awesome",
			"slow",
			"all powerful",
		}

		for _, c := range cases {
			t.Run(c, func(t *testing.T) {
				result, err := eval(t, "I am an {{ adjective }} LLM", ldcontext.New("user"), map[string]interface{}{"adjective": c})
				require.NoError(t, err)
				assert.Equal(t, "I am an "+c+" LLM", result)
			})
		}
	})

	t.Run("multiple variables", func(t *testing.T) {
		vars := map[string]interface{}{
			"adjective": "awesome",
			"noun":      "robot",
			"stats": map[string]interface{}{
				"power": "9000",
			},
		}
		result, err := eval(t, "I am an {{ adjective }} {{ noun }} with power over {{ stats.power }}", ldcontext.New("user"), vars)
		require.NoError(t, err)
		assert.Equal(t, "I am an awesome robot with power over 9000", result)
	})

	t.Run("interpolation with array indices does not work", func(t *testing.T) {
		vars := map[string]interface{}{
			"adjectives": []string{"awesome", "slow", "all powerful"},
		}

		t.Run("dot syntax interpolates as empty string", func(t *testing.T) {
			result, err := eval(t, "I am an ({{ adjectives.0 }}) LLM", ldcontext.New("user"), vars)
			require.NoError(t, err)
			assert.Equal(t, "I am an () LLM", result)
		})

		t.Run("bracket syntax returns error", func(t *testing.T) {
			_, err := eval(t, "I am an ({{ adjectives[0] }}) LLM", ldcontext.New("user"), vars)
			assert.Error(t, err)
		})
	})

	t.Run("array sections", func(t *testing.T) {
		vars := map[string]interface{}{
			"adjectives": []string{"hello", "world", "!"},
		}

		result, err := eval(t, "{{#adjectives }}{{ . }} {{/adjectives }}", ldcontext.New("user"), vars)
		require.NoError(t, err)
		assert.Equal(t, "hello world ! ", result)
	})

	t.Run("malformed syntax", func(t *testing.T) {
		_, err := eval(t, "This is a {{ malformed }]} prompt", ldcontext.New("user"), nil)
		require.Error(t, err)
	})

	t.Run("interpolate single kind context", func(t *testing.T) {
		context := ldcontext.NewBuilder("123").Name("Sandy").Build()
		result, err := eval(t, "I'm a {{ ldctx.kind}} with key {{ ldctx.key }}, named {{ ldctx.name }}", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "I'm a user with key 123, named Sandy", result)
	})

	t.Run("interpolation with nested context attributes", func(t *testing.T) {
		context := ldcontext.NewBuilder("123").
			SetValue("stats", ldvalue.ObjectBuild().Set("power", ldvalue.Int(9000)).Build()).Build()
		result, err := eval(t, "I can ingest over {{ ldctx.stats.power }} tokens per second!", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "I can ingest over 9000 tokens per second!", result)
	})

	t.Run("interpolation with multi kind context", func(t *testing.T) {
		user := ldcontext.NewBuilder("123").
			SetValue("cat_ownership", ldvalue.ObjectBuild().Set("count", ldvalue.Int(12)).Build()).Build()

		cat := ldcontext.NewBuilder("456").Kind("cat").
			SetValue("health", ldvalue.ObjectBuild().Set("hunger", ldvalue.String("off the charts")).Build()).Build()

		context := ldcontext.NewMulti(user, cat)

		result, err := eval(t, "As an owner of {{ ldctx.user.cat_ownership.count }} cats, I must report that my cat's hunger level is {{ ldctx.cat.health.hunger }}!", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "As an owner of 12 cats, I must report that my cat's hunger level is off the charts!", result)
	})

	t.Run("interpolation with multi kind context does not have anonymous attribute", func(t *testing.T) {
		user := ldcontext.NewBuilder("123").
			SetValue("cat_ownership", ldvalue.ObjectBuild().Set("count", ldvalue.Int(12)).Build()).Build()

		cat := ldcontext.NewBuilder("456").Kind("cat").
			SetValue("health", ldvalue.ObjectBuild().Set("hunger", ldvalue.String("off the charts")).Build()).Build()

		context := ldcontext.NewMulti(user, cat)

		result, err := eval(t, "anonymous=<{{ ldctx.anonymous }}>", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "anonymous=<>", result)
	})

	t.Run("interpolation with multi kind context has kind multi", func(t *testing.T) {
		user := ldcontext.NewBuilder("123").
			SetValue("cat_ownership", ldvalue.ObjectBuild().Set("count", ldvalue.Int(12)).Build()).Build()

		cat := ldcontext.NewBuilder("456").Kind("cat").
			SetValue("health", ldvalue.ObjectBuild().Set("hunger", ldvalue.String("off the charts")).Build()).Build()

		context := ldcontext.NewMulti(user, cat)

		result, err := eval(t, "kind=<{{ ldctx.kind }}>", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "kind=<multi>", result)
	})

	t.Run("interpolation with multi kind context does not have child kinds", func(t *testing.T) {

		// The idea here is that in a multi-kind context, we can access ldctx.kind (== "multi"), but you can't
		// access the kind field of the individual nested contexts since this doesn't match the actual data model.
		// That is, you can't access ldctx.user.kind or ldctx.cat.kind, only ldctx.kind.

		user := ldcontext.NewBuilder("123").
			SetValue("cat_ownership", ldvalue.ObjectBuild().Set("count", ldvalue.Int(12)).Build()).Build()

		cat := ldcontext.NewBuilder("456").Kind("cat").
			SetValue("health", ldvalue.ObjectBuild().Set("hunger", ldvalue.String("off the charts")).Build()).Build()

		context := ldcontext.NewMulti(user, cat)

		result, err := eval(t, "user_kind=<{{ ldctx.user.kind}}>,cat_kind=<{{ ldctx.cat.kind }}>", context, nil)
		require.NoError(t, err)
		assert.Equal(t, "user_kind=<>,cat_kind=<>", result)
	})
}

func TestModeFromMetadata(t *testing.T) {
	// Mode carried only in _ldMeta (no root-level "mode" key) must be returned by Mode().
	raw := []byte(`{"_ldMeta": {"variationKey": "v1", "enabled": true, "mode": "agent"}}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)
	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.Equal(t, "agent", cfg.Mode())
}

func TestParseJudgeSpecificFields(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"judgeConfiguration": {
			"judges": [
				{"key": "judge1", "samplingRate": 0.5},
				{"key": "judge2", "samplingRate": 1.0}
			]
		},
		"messages": [
			{"content": "test", "role": "system"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	assert.Equal(t, "judge", cfg.Mode())
	assert.Equal(t, "toxicity", cfg.EvaluationMetricKey())

	judgeConfig := cfg.JudgeConfiguration()
	require.NotNil(t, judgeConfig)
	require.Len(t, judgeConfig.Judges, 2)
	assert.Equal(t, "judge1", judgeConfig.Judges[0].Key)
	assert.Equal(t, 0.5, judgeConfig.Judges[0].SamplingRate)
	assert.Equal(t, "judge2", judgeConfig.Judges[1].Key)
	assert.Equal(t, 1.0, judgeConfig.Judges[1].SamplingRate)
}

func TestParseEvaluationMetricKeys(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKeys": ["relevance", "accuracy"],
		"messages": [
			{"content": "test", "role": "system"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	assert.Equal(t, "judge", cfg.Mode())
	assert.Equal(t, "", cfg.EvaluationMetricKey())
	assert.Equal(t, []string{"relevance", "accuracy"}, cfg.EvaluationMetricKeys())
}

func TestParseEvaluationMetricKeyPriority(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"evaluationMetricKeys": ["relevance", "accuracy"],
		"messages": [
			{"content": "test", "role": "system"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	assert.Equal(t, "judge", cfg.Mode())
	// Both fields should be parsed
	assert.Equal(t, "toxicity", cfg.EvaluationMetricKey())
	assert.Equal(t, []string{"relevance", "accuracy"}, cfg.EvaluationMetricKeys())
}

func TestJudgeConfigurationImmutable(t *testing.T) {
	// Test that mutations to JudgeConfiguration don't affect the Config
	judgeConfig := &datamodel.JudgeConfiguration{
		Judges: []datamodel.Judge{
			{Key: "judge1", SamplingRate: 0.5},
			{Key: "judge2", SamplingRate: 1.0},
		},
	}

	builder := NewConfig().
		Enable().
		WithJudgeConfiguration(judgeConfig)
	cfg := builder.Build()

	// Mutate the original
	judgeConfig.Judges[0].Key = "mutated"
	judgeConfig.Judges = append(judgeConfig.Judges, datamodel.Judge{Key: "judge3", SamplingRate: 0.3})

	// Config should not be affected
	retrieved := cfg.JudgeConfiguration()
	require.NotNil(t, retrieved)
	require.Len(t, retrieved.Judges, 2)
	assert.Equal(t, "judge1", retrieved.Judges[0].Key) // Should still be original value
	assert.Equal(t, "judge2", retrieved.Judges[1].Key)

	// Mutate the retrieved config
	retrieved.Judges[0].Key = "mutated_again"
	retrieved.Judges = append(retrieved.Judges, datamodel.Judge{Key: "judge4", SamplingRate: 0.4})

	// Config should still not be affected
	retrieved2 := cfg.JudgeConfiguration()
	require.NotNil(t, retrieved2)
	require.Len(t, retrieved2.Judges, 2)
	assert.Equal(t, "judge1", retrieved2.Judges[0].Key) // Should still be original value
	assert.Equal(t, "judge2", retrieved2.Judges[1].Key)
}

// TestJudgeConfig_PreservesReservedPlaceholders verifies that JudgeConfig injects reserved variables
// so that {{message_history}} and {{response_to_evaluate}} are preserved for the second interpolation
// pass during Judge.Evaluate(). Without this, Config's first Mustache pass would render them as empty.
func TestJudgeConfig_PreservesReservedPlaceholders(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"messages": [
			{"content": "You are a judge.", "role": "system"},
			{"content": "Input: {{message_history}}\nOutput: {{response_to_evaluate}}", "role": "user"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), NewAIJudgeConfigDefault(), nil)

	msgs := cfg.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "You are a judge.", msgs[0].Content)
	assert.Contains(t, msgs[1].Content, "{{message_history}}", "JudgeConfig must preserve placeholder for second interpolation")
	assert.Contains(t, msgs[1].Content, "{{response_to_evaluate}}", "JudgeConfig must preserve placeholder for second interpolation")
	assert.Equal(t, "Input: {{message_history}}\nOutput: {{response_to_evaluate}}", msgs[1].Content)
}

// TestConfig_WithoutReservedVarsWipesJudgePlaceholders documents that Config (without reserved vars)
// renders {{message_history}} and {{response_to_evaluate}} as empty when used for judge templates.
func TestConfig_WithoutReservedVarsWipesJudgePlaceholders(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"messages": [
			{"content": "Input: {{message_history}}\nOutput: {{response_to_evaluate}}", "role": "user"}
		]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)
	require.NotNil(t, client)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	msgs := cfg.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "Input: \nOutput: ", msgs[0].Content, "Config without reserved vars renders placeholders as empty")
}

func TestCreateTracker_ManuallyBuiltConfig_ReturnsNil(t *testing.T) {
	cfg := NewConfig().Enable().WithMessage("hello", datamodel.User).Build()
	assert.Nil(t, cfg.CreateTracker(), "manually built config should not have a tracker factory")
}

func TestCreateTracker_DisabledConfig_ReturnsTracker(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": false},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.False(t, cfg.Enabled())
	assert.NotNil(t, cfg.CreateTracker(), "disabled config should still have a tracker factory")
}

func TestCreateTracker_EnabledConfig_ReturnsTracker(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"model": {"name": "gpt-4"},
		"provider": {"name": "openai"},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)
	assert.True(t, cfg.Enabled())

	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker, "enabled config should have a tracker factory")
}

func TestCreateTracker_FreshRunIdPerCall(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	// Clear SDK info event
	mockSDK.events = nil

	cfg := client.CompletionConfig("key", ldcontext.New("user"), Disabled(), nil)

	tracker1 := cfg.CreateTracker()
	tracker2 := cfg.CreateTracker()
	require.NotNil(t, tracker1)
	require.NotNil(t, tracker2)

	// Each tracker should be able to track independently. Track success on both to emit events.
	_ = tracker1.TrackSuccess()
	_ = tracker2.TrackSuccess()

	// Filter out the usage event; we only want the generation events.
	var genEvents []mockEvent
	for _, e := range mockSDK.events {
		if e.eventName == "$ld:ai:generation:success" {
			genEvents = append(genEvents, e)
		}
	}

	require.Len(t, genEvents, 2, "each tracker should emit its own event")

	runId1 := genEvents[0].data.GetByKey("runId").StringValue()
	runId2 := genEvents[1].data.GetByKey("runId").StringValue()
	assert.NotEmpty(t, runId1)
	assert.NotEmpty(t, runId2)
	assert.NotEqual(t, runId1, runId2, "each tracker must have a unique runId")
}

func TestCreateTracker_TrackerHasCorrectMetadata(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true, "version": 5},
		"model": {"name": "gpt-4"},
		"provider": {"name": "openai"},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	mockSDK := newMockSDK(json, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	// Clear SDK info event
	mockSDK.events = nil

	cfg := client.CompletionConfig("my-config", ldcontext.New("user"), Disabled(), nil)

	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker)

	_ = tracker.TrackSuccess()

	// Filter for the generation event (skip usage event)
	var genEvent *mockEvent
	for i, e := range mockSDK.events {
		if e.eventName == "$ld:ai:generation:success" {
			genEvent = &mockSDK.events[i]
			break
		}
	}
	require.NotNil(t, genEvent)

	data := genEvent.data
	assert.Equal(t, "my-config", data.GetByKey("configKey").StringValue())
	assert.Equal(t, "var-1", data.GetByKey("variationKey").StringValue())
	assert.Equal(t, 5, data.GetByKey("version").IntValue())
	assert.Equal(t, "openai", data.GetByKey("providerName").StringValue())
	assert.Equal(t, "gpt-4", data.GetByKey("modelName").StringValue())
	assert.NotEmpty(t, data.GetByKey("runId").StringValue())
}

func TestCreateTracker_JudgeConfigHasFactory(t *testing.T) {
	json := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"messages": [{"content": "test", "role": "system"}]
	}`)

	client, err := NewClient(newMockSDK(json, nil))
	require.NoError(t, err)

	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), NewAIJudgeConfigDefault(), nil)
	assert.True(t, cfg.Enabled())

	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker, "enabled judge config should have a tracker factory")
}

// TestJudgeConfig_TypedReturn verifies that JudgeConfig returns a correctly populated AIJudgeConfig
// with the right field values from the wire payload.
func TestJudgeConfig_TypedReturn(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v-judge", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "toxicity",
		"model": {"name": "judge-model"},
		"provider": {"name": "judge-provider"},
		"messages": [
			{"content": "Hello {{name}}", "role": "system"}
		]
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), NewAIJudgeConfigDefault(), map[string]interface{}{"name": "World"})

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "toxicity", cfg.EvaluationMetricKey())
	assert.Equal(t, "judge-model", cfg.Model().Name)
	assert.Equal(t, "judge-provider", cfg.Provider().Name)
	msgs := cfg.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello World", msgs[0].Content)
}

// TestJudgeConfig_DefaultPath verifies that the error/offline path returns an AIJudgeConfig built
// from the provided AIJudgeConfigDefault, with a working CreateTracker().
func TestJudgeConfig_DefaultPath(t *testing.T) {
	mockSDK := newMockSDK(nil, fmt.Errorf("offline"))
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIJudgeConfigDefault().
		WithModelName("fallback-model").
		WithProviderName("fallback-provider").
		WithEvaluationMetricKey("accuracy")

	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), def, nil)

	assert.True(t, cfg.Enabled(), "default path should be enabled by default")
	assert.Equal(t, "fallback-model", cfg.Model().Name)
	assert.Equal(t, "fallback-provider", cfg.Provider().Name)
	assert.Equal(t, "accuracy", cfg.EvaluationMetricKey())
	assert.NotNil(t, cfg.CreateTracker(), "default-path judge config must have a working tracker factory")
}

// TestJudgeConfig_TrackerEmitsCorrectTrackData verifies that the tracker produced from a judge config
// emits the expected model/provider/configKey/version fields.
func TestJudgeConfig_TrackerEmitsCorrectTrackData(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v-j", "enabled": true, "version": 3, "mode": "judge"},
		"evaluationMetricKey": "relevance",
		"model": {"name": "judge-model"},
		"provider": {"name": "judge-provider"},
		"messages": [{"content": "test", "role": "system"}]
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	ctx := ldcontext.New("user-key")
	cfg := client.JudgeConfig("judge-config", ctx, NewAIJudgeConfigDefault(), nil)
	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker)
	require.NoError(t, tracker.TrackSuccess())

	var genEvent *mockEvent
	for i, e := range mockSDK.events {
		if e.eventName == "$ld:ai:generation:success" {
			genEvent = &mockSDK.events[i]
			break
		}
	}
	require.NotNil(t, genEvent)
	data := genEvent.data
	assert.Equal(t, "judge-config", data.GetByKey("configKey").StringValue())
	assert.Equal(t, "v-j", data.GetByKey("variationKey").StringValue())
	assert.Equal(t, 3, data.GetByKey("version").IntValue())
	assert.Equal(t, "judge-provider", data.GetByKey("providerName").StringValue())
	assert.Equal(t, "judge-model", data.GetByKey("modelName").StringValue())
	assert.NotEmpty(t, data.GetByKey("runId").StringValue())
}

// TestJudgeConfig_CompletionUnchanged is a regression guard verifying that CompletionConfig
// still returns AICompletionConfig with its full field set after the evaluateShared refactor.
func TestJudgeConfig_CompletionUnchanged(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v-c", "enabled": true},
		"evaluationMetricKey": "score",
		"model": {"name": "comp-model"},
		"provider": {"name": "comp-provider"},
		"messages": [{"content": "Hi {{who}}", "role": "user"}]
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.CompletionConfig("comp-key", ldcontext.New("user"), Disabled(), map[string]interface{}{"who": "there"})

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "comp-model", cfg.Model().Name)
	assert.Equal(t, "comp-provider", cfg.Provider().Name)
	assert.Equal(t, "score", cfg.EvaluationMetricKey())
	msgs := cfg.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hi there", msgs[0].Content)
	assert.NotNil(t, cfg.CreateTracker())
}

func TestJudgeConfig_EvaluationMetricKeyFallback(t *testing.T) {
	makeClient := func(t *testing.T, raw []byte) *Client {
		t.Helper()
		client, err := NewClient(newMockSDK(raw, nil))
		require.NoError(t, err)
		return client
	}
	ctx := ldcontext.New("user")
	def := NewAIJudgeConfigDefault()

	t.Run("only plural entry → returns first non-empty plural", func(t *testing.T) {
		raw := []byte(`{
			"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
			"evaluationMetricKeys": ["$ld:ai:judge:x"],
			"messages": [{"content": "test", "role": "system"}]
		}`)
		cfg := makeClient(t, raw).JudgeConfig("k", ctx, def, nil)
		assert.Equal(t, "$ld:ai:judge:x", cfg.EvaluationMetricKey())
	})

	t.Run("both present → singular wins", func(t *testing.T) {
		raw := []byte(`{
			"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
			"evaluationMetricKey": "singular",
			"evaluationMetricKeys": ["plural"],
			"messages": [{"content": "test", "role": "system"}]
		}`)
		cfg := makeClient(t, raw).JudgeConfig("k", ctx, def, nil)
		assert.Equal(t, "singular", cfg.EvaluationMetricKey())
	})

	t.Run("whitespace singular + valid plural → returns first non-empty plural", func(t *testing.T) {
		raw := []byte(`{
			"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
			"evaluationMetricKey": "   ",
			"evaluationMetricKeys": ["", "  ", "fallback"],
			"messages": [{"content": "test", "role": "system"}]
		}`)
		cfg := makeClient(t, raw).JudgeConfig("k", ctx, def, nil)
		assert.Equal(t, "fallback", cfg.EvaluationMetricKey())
	})

	t.Run("neither present → empty key", func(t *testing.T) {
		raw := []byte(`{
			"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
			"messages": [{"content": "test", "role": "system"}]
		}`)
		cfg := makeClient(t, raw).JudgeConfig("k", ctx, def, nil)
		assert.Equal(t, "", cfg.EvaluationMetricKey())
	})
}

func TestClient_CreateTracker_RoundTrip(t *testing.T) {
	configJSON := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true, "version": 5},
		"model": {"name": "gpt-4"},
		"provider": {"name": "openai"},
		"messages": [{"content": "hello", "role": "user"}]
	}`)

	mockSDK := newMockSDK(configJSON, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	// Clear SDK info event
	mockSDK.events = nil

	cfg := client.CompletionConfig("my-config", ldcontext.New("user"), Disabled(), nil)
	originalTracker := cfg.CreateTracker()
	require.NotNil(t, originalTracker)

	token := originalTracker.ResumptionToken()
	require.NotEmpty(t, token)

	// Reconstruct from token with a different context
	newContext := ldcontext.New("other-user")
	reconstructed, err := client.CreateTracker(token, newContext)
	require.NoError(t, err)
	require.NotNil(t, reconstructed)

	// The reconstructed tracker should produce the same resumption token
	assert.Equal(t, token, reconstructed.ResumptionToken())

	// Track feedback on the reconstructed tracker and verify it uses the original runId
	_ = originalTracker.TrackSuccess()
	_ = reconstructed.TrackFeedback(FeedbackPositive)

	var successEvent, feedbackEvent *mockEvent
	for i, e := range mockSDK.events {
		switch e.eventName {
		case "$ld:ai:generation:success":
			successEvent = &mockSDK.events[i]
		case "$ld:ai:feedback:user:positive":
			feedbackEvent = &mockSDK.events[i]
		}
	}
	require.NotNil(t, successEvent)
	require.NotNil(t, feedbackEvent)

	// Both events should share the same runId
	originalRunId := successEvent.data.GetByKey("runId").StringValue()
	reconstructedRunId := feedbackEvent.data.GetByKey("runId").StringValue()
	assert.Equal(t, originalRunId, reconstructedRunId, "reconstructed tracker must reuse the original runId")

	// Reconstructed tracker should use the new context
	assert.Equal(t, newContext, feedbackEvent.context)

	// Verify metadata preserved
	assert.Equal(t, "my-config", feedbackEvent.data.GetByKey("configKey").StringValue())
	assert.Equal(t, "var-1", feedbackEvent.data.GetByKey("variationKey").StringValue())
	assert.Equal(t, 5, feedbackEvent.data.GetByKey("version").IntValue())

	// modelName and providerName should be empty on reconstructed tracker
	assert.Equal(t, "", feedbackEvent.data.GetByKey("modelName").StringValue())
	assert.Equal(t, "", feedbackEvent.data.GetByKey("providerName").StringValue())

	// SDK identification must be present on events from both trackers.
	for _, evt := range []*mockEvent{successEvent, feedbackEvent} {
		assert.Equal(t, SDKName, evt.data.GetByKey("aiSdkName").StringValue())
		assert.Equal(t, Version, evt.data.GetByKey("aiSdkVersion").StringValue())
	}
}

func TestClient_CreateTracker_InvalidToken(t *testing.T) {
	mockSDK := newMockSDK(nil, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	t.Run("invalid base64", func(t *testing.T) {
		_, err := client.CreateTracker("not-valid-base64!!!", ldcontext.New("user"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid resumption token")
	})

	t.Run("valid base64 but invalid JSON", func(t *testing.T) {
		token := base64.RawURLEncoding.EncodeToString([]byte("not json"))
		_, err := client.CreateTracker(token, ldcontext.New("user"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid resumption token")
	})

	t.Run("valid token with missing fields uses zero values", func(t *testing.T) {
		payload, _ := json.Marshal(map[string]interface{}{"runId": "test-run"})
		token := base64.RawURLEncoding.EncodeToString(payload)
		tracker, err := client.CreateTracker(token, ldcontext.New("user"))
		require.NoError(t, err)
		require.NotNil(t, tracker)

		// Should work with partial data
		resumeToken := tracker.ResumptionToken()
		assert.NotEmpty(t, resumeToken)
	})
}

func TestClient_CreateTracker_RoundTrip_WithGraphKey(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	originalTracker := newTracker(events, newRunID(), "my-config", "var-1", 5, ldcontext.New("user"), config, nil, "my-graph")

	token := originalTracker.ResumptionToken()
	require.NotEmpty(t, token)

	mockSDK := newMockSDK(nil, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	reconstructed, err := client.CreateTracker(token, ldcontext.New("other-user"))
	require.NoError(t, err)
	require.NotNil(t, reconstructed)

	assert.Equal(t, token, reconstructed.ResumptionToken())

	assert.NoError(t, reconstructed.TrackSuccess())

	var successEvent *mockEvent
	for i, e := range mockSDK.events {
		if e.eventName == "$ld:ai:generation:success" {
			successEvent = &mockSDK.events[i]
			break
		}
	}
	require.NotNil(t, successEvent)
	assert.Equal(t, "my-graph", successEvent.data.GetByKey("graphKey").StringValue())
	assert.Equal(t, "my-config", successEvent.data.GetByKey("configKey").StringValue())
}

// ---- AgentConfig tests ----

// TestAgentConfig_ValidFlag verifies that AgentConfig returns a correctly populated AIAgentConfig.
func TestAgentConfig_ValidFlag(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "v-agent", "enabled": true, "version": 2, "mode": "agent"},
		"model": {"name": "agent-model"},
		"provider": {"name": "agent-provider"},
		"instructions": "You are a helpful assistant.",
		"tools": {
			"search": {"name": "search", "description": "Search the web"}
		}
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), NewAIAgentConfigDefault(), nil)

	assert.True(t, cfg.Enabled())
	assert.Equal(t, "v-agent", cfg.variationKey)
	assert.Equal(t, 2, cfg.version)
	assert.Equal(t, "agent-model", cfg.Model().Name)
	assert.Equal(t, "agent-provider", cfg.Provider().Name)
	assert.Equal(t, "You are a helpful assistant.", cfg.Instructions())
	require.Len(t, cfg.Tools(), 1)
	assert.Equal(t, "search", cfg.Tools()["search"].Name())
	assert.NotNil(t, cfg.CreateTracker())
}

// TestAgentConfig_UsageTracking verifies that AgentConfig emits only the agent-config usage event.
func TestAgentConfig_UsageTracking(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "Hello."
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	ctx := ldcontext.New("user-key")
	configKey := "agent-config-key"
	client.AgentConfig(configKey, ctx, NewAIAgentConfigDefault(), nil)

	expectedData := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(configKey)).Build()
	require.Len(t, mockSDK.events, 1)
	evt := mockSDK.events[0]
	assert.Equal(t, "$ld:ai:usage:agent-config", evt.eventName)
	assert.Equal(t, ctx, evt.context)
	assert.Equal(t, float64(1), evt.metricValue)
	assert.Equal(t, expectedData, evt.data)
}

// TestAgentConfig_DisabledFlag verifies that a disabled flag returns Enabled() == false.
func TestAgentConfig_DisabledFlag(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": false, "mode": "agent"},
		"instructions": "Disabled agent."
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), NewAIAgentConfigDefault(), nil)

	assert.False(t, cfg.Enabled())
	assert.NotNil(t, cfg.CreateTracker())
}

// TestAgentConfig_DefaultPath verifies that the error/offline path returns an AIAgentConfig built
// from the provided AIAgentConfigDefault, with a working CreateTracker().
func TestAgentConfig_DefaultPath(t *testing.T) {
	mockSDK := newMockSDK(nil, fmt.Errorf("offline"))
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIAgentConfigDefault().
		WithModelName("fallback-model").
		WithProviderName("fallback-provider").
		WithInstructions("Fallback instructions.")

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), def, nil)

	assert.True(t, cfg.Enabled(), "default path should be enabled by default")
	assert.Equal(t, "fallback-model", cfg.Model().Name)
	assert.Equal(t, "fallback-provider", cfg.Provider().Name)
	assert.Equal(t, "Fallback instructions.", cfg.Instructions())
	assert.NotNil(t, cfg.CreateTracker())
}

// TestAgentConfig_InstructionsInterpolation verifies that {{variable}} in instructions is
// replaced with the provided variable value.
func TestAgentConfig_InstructionsInterpolation(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "Hello, {{name}}! You assist {{role}} users."
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"),
		NewAIAgentConfigDefault(),
		map[string]interface{}{"name": "Alice", "role": "enterprise"},
	)

	assert.Equal(t, "Hello, Alice! You assist enterprise users.", cfg.Instructions())
}

// TestAgentConfig_InstructionsInterpolation_NoVariables verifies that instructions without
// variables are returned verbatim (no interpolation pass is attempted).
func TestAgentConfig_InstructionsInterpolation_NoVariables(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "Static instructions."
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), NewAIAgentConfigDefault(), nil)

	assert.Equal(t, "Static instructions.", cfg.Instructions())
}

// TestAgentConfig_InstructionsInterpolation_LdCtx verifies that ldctx context attributes are
// available in instructions templates.
func TestAgentConfig_InstructionsInterpolation_LdCtx(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "User key is {{ldctx.key}}."
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	ctx := ldcontext.New("user-123")
	cfg := client.AgentConfig("agent-key", ctx, NewAIAgentConfigDefault(), map[string]interface{}{})

	assert.Equal(t, "User key is user-123.", cfg.Instructions())
}

// TestAgentConfig_GraphKeyPassthrough verifies that a non-empty graphKey flows from
// evaluateAgentConfig into the tracker's event data.
func TestAgentConfig_GraphKeyPassthrough(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "Test."
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	ctx := ldcontext.New("user")
	cfg := client.evaluateAgentConfig("agent-key", ctx, NewAIAgentConfigDefault(), nil, "my-graph")

	tracker := cfg.CreateTracker()
	require.NotNil(t, tracker)
	require.NoError(t, tracker.TrackSuccess())

	var genEvent *mockEvent
	for i, e := range mockSDK.events {
		if e.eventName == "$ld:ai:generation:success" {
			genEvent = &mockSDK.events[i]
			break
		}
	}
	require.NotNil(t, genEvent)
	assert.Equal(t, "my-graph", genEvent.data.GetByKey("graphKey").StringValue())
}

// TestAgentConfig_ModeMismatch verifies that a flag with the wrong mode falls back to the default
// and a warning is logged.
func TestAgentConfig_ModeMismatch(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "judge"},
		"instructions": "Should not be used."
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIAgentConfigDefault().WithInstructions("Default instructions.")
	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), def, nil)

	assert.Equal(t, "Default instructions.", cfg.Instructions(),
		"mode mismatch must fall back to default instructions")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "expected mode")
}

// TestAgentConfig_ModeEmptyFallsBack verifies that a flag with no mode field falls back to the
// default and logs a warning, because a missing mode is treated as "completion" per spec.
func TestAgentConfig_ModeEmptyFallsBack(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"instructions": "Should not be used."
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), NewAIAgentConfigDefault(), nil)

	assert.Equal(t, "", cfg.Instructions(),
		"empty mode must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "expected mode")
}

// ---- AgentConfigs batch tests ----

// TestAgentConfigs_Batch verifies that all requests are resolved with their own defaults/variables.
func TestAgentConfigs_Batch(t *testing.T) {
	// Use an error SDK so every key falls back to its default; simplifies assertions.
	mockSDK := newMockSDK(nil, fmt.Errorf("offline"))
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	requests := []AgentConfigRequest{
		{Key: "agent-a", DefaultValue: NewAIAgentConfigDefault().WithInstructions("A instructions.")},
		{Key: "agent-b", DefaultValue: NewAIAgentConfigDefault().WithInstructions("B instructions.")},
		{Key: "agent-c"}, // zero-value DefaultValue
	}

	result := client.AgentConfigs(requests, ldcontext.New("user"))

	require.Len(t, result, 3)
	// Map values are not addressable, so copy to local vars before calling pointer receivers.
	cfgA, cfgB, cfgC := result["agent-a"], result["agent-b"], result["agent-c"]
	assert.Equal(t, "A instructions.", cfgA.Instructions())
	assert.Equal(t, "B instructions.", cfgB.Instructions())
	assert.Equal(t, "", cfgC.Instructions(), "missing key uses zero-value default")
	assert.NotNil(t, cfgA.CreateTracker())
	assert.NotNil(t, cfgB.CreateTracker())
	assert.NotNil(t, cfgC.CreateTracker())

	// Verify the single batch usage event was emitted with the request count.
	require.Len(t, mockSDK.events, 1)
	evt := mockSDK.events[0]
	assert.Equal(t, "$ld:ai:usage:agent-configs", evt.eventName)
	assert.Equal(t, float64(3), evt.metricValue)
	assert.Equal(t, ldvalue.Int(3), evt.data)
}

// TestAgentConfigs_DoesNotEmitPerKeyUsageEvents verifies that AgentConfigs does not fire
// $ld:ai:usage:agent-config events per key — only the single batch event is emitted.
func TestAgentConfigs_DoesNotEmitPerKeyUsageEvents(t *testing.T) {
	mockSDK := newMockSDK(nil, fmt.Errorf("offline"))
	client, err := NewClient(mockSDK)
	require.NoError(t, err)
	mockSDK.events = nil

	client.AgentConfigs(
		[]AgentConfigRequest{{Key: "k1"}, {Key: "k2"}},
		ldcontext.New("user"),
	)

	for _, e := range mockSDK.events {
		assert.NotEqual(t, "$ld:ai:usage:agent-config", e.eventName,
			"AgentConfigs must not emit per-key agent-config events")
	}
}

// ---- Tools parity tests ----

// TestToolsParity_AgentConfig verifies that Tools() is consistent between the success path and
// the default/offline path for AgentConfig.
func TestToolsParity_AgentConfig(t *testing.T) {
	toolsJSON := `"tools": {"search": {"name": "search", "description": "Search the web"}}`

	// Success path: tools come from the flag JSON.
	successRaw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"instructions": "Test.",` + toolsJSON + `
	}`)
	client, err := NewClient(newMockSDK(successRaw, nil))
	require.NoError(t, err)
	successCfg := client.AgentConfig("k", ldcontext.New("user"), NewAIAgentConfigDefault(), nil)

	// Default/offline path: tools come from AIAgentConfigDefault.
	offlineClient, err := NewClient(newMockSDK(nil, fmt.Errorf("offline")))
	require.NoError(t, err)
	defWithTool := NewAIAgentConfigDefault().WithTool(datamodel.Tool{Name: "search", Description: "Search the web"})
	defaultCfg := offlineClient.AgentConfig("k", ldcontext.New("user"), defWithTool, nil)

	assert.Equal(t, len(successCfg.Tools()), len(defaultCfg.Tools()),
		"Tools() count must match between success and default paths")
	assert.Equal(t, "search", successCfg.Tools()["search"].Name())
	assert.Equal(t, "search", defaultCfg.Tools()["search"].Name())
}

// ---- JudgeConfig mode-mismatch tests ----

// TestJudgeConfig_ModeMismatch verifies that a flag with the wrong mode falls back to the
// default and logs a warning.
func TestJudgeConfig_ModeMismatch(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true, "mode": "agent"},
		"messages": [{"content": "test", "role": "system"}]
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIJudgeConfigDefault().WithEvaluationMetricKey("accuracy")
	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), def, nil)

	assert.Equal(t, "accuracy", cfg.EvaluationMetricKey(),
		"mode mismatch must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "expected mode")
}

// TestJudgeConfig_ModeEmptyFallsBack verifies that a judge flag with no mode falls back to the
// default and logs a warning, because a missing mode is treated as "completion" per spec.
func TestJudgeConfig_ModeEmptyFallsBack(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "1", "enabled": true},
		"evaluationMetricKey": "toxicity",
		"messages": [{"content": "test", "role": "system"}]
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), NewAIJudgeConfigDefault(), nil)

	assert.Equal(t, "", cfg.EvaluationMetricKey(),
		"empty mode must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "expected mode")
}

// ---- Message interpolation error tests ----

// TestAgentConfig_BadMessagesGoodInstructions verifies that a served agent flag with a malformed
// messages entry but a valid instructions template returns the served config — not the default.
// Agent configs use instructions, not messages, so messages interpolation must not be attempted.
func TestAgentConfig_BadMessagesGoodInstructions(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true, "mode": "agent"},
		"model": {"name": "gpt-4o", "provider": {"name": "openai"}},
		"messages": [{"content": "{{ bad }]}", "role": "user"}],
		"instructions": "Hello, {{name}}!"
	}`)
	client, err := NewClient(newMockSDK(raw, nil))
	require.NoError(t, err)

	def := NewAIAgentConfigDefault().WithInstructions("Default instructions.")
	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), def,
		map[string]interface{}{"name": "world"})

	assert.True(t, cfg.Enabled(), "served config must be enabled")
	assert.Equal(t, "Hello, world!", cfg.Instructions(),
		"instructions must be interpolated from the served flag, not the default")
	assert.Equal(t, "gpt-4o", cfg.ModelName(),
		"model must come from the served flag, not the default")
}

// TestAgentConfig_BadInstructionsFallsBack verifies that a malformed instructions template still
// causes evaluateAgentConfig to fall back to the default.
func TestAgentConfig_BadInstructionsFallsBack(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true, "mode": "agent"},
		"instructions": "{{ bad }]}"
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIAgentConfigDefault().WithInstructions("Default instructions.")
	cfg := client.AgentConfig("agent-key", ldcontext.New("user"), def, nil)

	assert.Equal(t, "Default instructions.", cfg.Instructions(),
		"malformed instructions must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "malformed instructions template")
}

// TestCompletionConfig_BadMessagesFallsBack verifies that a completion flag with a malformed
// message template still falls back to the default (regression guard — behavior unchanged).
func TestCompletionConfig_BadMessagesFallsBack(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true},
		"messages": [{"content": "{{ bad }]}", "role": "user"}]
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAICompletionConfigDefault().WithMessage("Default message.", datamodel.User)
	cfg := client.CompletionConfig("completion-key", ldcontext.New("user"), def, nil)

	assert.Equal(t, def.AsLdValue(), cfg.AsLdValue(),
		"malformed completion message must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "malformed message at index")
}

// TestJudgeConfig_BadMessagesFallsBack verifies that a judge flag with a malformed message
// template still falls back to the default (regression guard — behavior unchanged).
func TestJudgeConfig_BadMessagesFallsBack(t *testing.T) {
	raw := []byte(`{
		"_ldMeta": {"variationKey": "var-1", "enabled": true, "mode": "judge"},
		"evaluationMetricKey": "accuracy",
		"messages": [{"content": "{{ bad }]}", "role": "system"}]
	}`)
	mockSDK := newMockSDK(raw, nil)
	client, err := NewClient(mockSDK)
	require.NoError(t, err)

	def := NewAIJudgeConfigDefault().WithEvaluationMetricKey("default-metric")
	cfg := client.JudgeConfig("judge-key", ldcontext.New("user"), def, nil)

	assert.Equal(t, "default-metric", cfg.EvaluationMetricKey(),
		"malformed judge message must fall back to default")
	mockSDK.log.AssertMessageMatch(t, true, ldlog.Warn, "malformed message at index")
}
