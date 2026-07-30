package ldai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldlogtest"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
)

type mockEvents struct {
	log    *ldlogtest.MockLog
	events []trackEvent
}

type trackEvent struct {
	name        string
	context     ldcontext.Context
	metricValue float64
	data        ldvalue.Value
}

func newMockEvents() *mockEvents {
	return &mockEvents{log: ldlogtest.NewMockLog()}
}

func (m *mockEvents) TrackMetric(eventName string, context ldcontext.Context, metricValue float64, data ldvalue.Value) error {
	m.events = append(m.events, trackEvent{name: eventName, context: context, metricValue: metricValue, data: data})
	return nil
}

func TestTracker_NewPanicsWithNilConfig(t *testing.T) {
	assert.Panics(t, func() {
		newTracker(newMockEvents(), newRunID(), "key", "variationKey", 1, ldcontext.New("key"), nil, nil, "")
	})
}

func TestTracker_NewDoesNotPanicWithConfig(t *testing.T) {
	assert.NotPanics(t, func() {
		newTracker(newMockEvents(), newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")
	})
}

func makeTrackData(configKey, variationKey string, version int, config *Config, runId string, graphKey string) ldvalue.Value {
	builder := ldvalue.ObjectBuild().
		Set("runId", ldvalue.String(runId)).
		Set("configKey", ldvalue.String(configKey)).
		Set("version", ldvalue.Int(version)).
		Set("providerName", ldvalue.String(config.ProviderName())).
		Set("modelName", ldvalue.String(config.ModelName())).
		Set("aiSdkName", ldvalue.String(SDKName)).
		Set("aiSdkVersion", ldvalue.String(Version))
	if variationKey != "" {
		builder.Set("variationKey", ldvalue.String(variationKey))
	}
	if graphKey != "" {
		builder.Set("graphKey", ldvalue.String(graphKey))
	}
	return builder.Build()
}

func extractRunId(t *testing.T, events *mockEvents) string {
	t.Helper()
	require.NotEmpty(t, events.events, "expected at least one event to extract runId")
	runId := events.events[0].data.GetByKey("runId").StringValue()
	require.NotEmpty(t, runId, "expected runId to be non-empty")
	return runId
}

func TestTracker_TrackSuccess(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")
	assert.NoError(t, tracker.TrackSuccess())

	runId := extractRunId(t, events)
	expectedEvents := []trackEvent{
		{
			name:        "$ld:ai:generation:success",
			context:     ldcontext.New("key"),
			metricValue: 1.0,
			data:        makeTrackData("key", "variationKey", 1, config, runId, ""),
		},
	}

	assert.ElementsMatch(t, expectedEvents, events.events)
}

func TestTracker_TrackError(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 2, ldcontext.New("key"), config, nil, "")
	assert.NoError(t, tracker.TrackError())

	runId := extractRunId(t, events)
	expectedEvents := []trackEvent{
		{
			name:        "$ld:ai:generation:error",
			context:     ldcontext.New("key"),
			metricValue: 1.0,
			data:        makeTrackData("key", "variationKey", 2, config, runId, ""),
		},
	}

	assert.ElementsMatch(t, expectedEvents, events.events)
}

func TestTracker_TrackRequest(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 3, ldcontext.New("key"), config, nil, "")

	expectedResponse := ProviderResponse{
		Usage: TokenUsage{
			Total: 1,
		},
		Metrics: Metrics{
			Latency:          10 * time.Millisecond,
			TimeToFirstToken: 42 * time.Millisecond,
		},
	}

	r, err := tracker.TrackRequest(func(c *Config) (ProviderResponse, error) {
		return expectedResponse, nil
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, r)

	runId := extractRunId(t, events)
	expectedEvents := []trackEvent{
		{
			name:        "$ld:ai:generation:success",
			context:     ldcontext.New("key"),
			metricValue: 1,
			data:        makeTrackData("key", "variationKey", 3, config, runId, ""),
		},
		{
			name:        "$ld:ai:duration:total",
			context:     ldcontext.New("key"),
			metricValue: 10.0,
			data:        makeTrackData("key", "variationKey", 3, config, runId, ""),
		},
		{
			name:        "$ld:ai:tokens:total",
			context:     ldcontext.New("key"),
			metricValue: 1,
			data:        makeTrackData("key", "variationKey", 3, config, runId, ""),
		},
		{
			name:        "$ld:ai:tokens:ttf",
			context:     ldcontext.New("key"),
			metricValue: 42.0,
			data:        makeTrackData("key", "variationKey", 3, config, runId, ""),
		},
	}

	assert.ElementsMatch(t, expectedEvents, events.events)
}

func TestTracker_TrackRequestReceivesConfig(t *testing.T) {
	events := newMockEvents()

	expectedConfig := AICompletionConfig{
		aiConfigBase: aiConfigBase{
			enabled:  true,
			version:  defaultVersion(nil),
			model:    ModelConfig{Name: "model", Parameters: map[string]ldvalue.Value{"param": ldvalue.String("value")}, Custom: map[string]ldvalue.Value{"custom": ldvalue.String("value")}},
			provider: ProviderConfig{Name: "provider"},
		},
		messages: []datamodel.Message{{Content: "hello", Role: datamodel.Assistant}},
	}

	tracker := newTracker(events, newRunID(), "key", "variationKey", 4, ldcontext.New("key"), &expectedConfig, nil, "")

	var gotConfig *Config
	_, _ = tracker.TrackRequest(func(c *Config) (ProviderResponse, error) {
		gotConfig = c
		return ProviderResponse{}, nil
	})

	assert.Equal(t, expectedConfig, *gotConfig)
}

type mockStopwatch time.Duration

func (m mockStopwatch) Start() {}

func (m mockStopwatch) Stop() time.Duration {
	return time.Duration(m)
}

func TestTracker_LatencyMeasuredIfNotProvided(t *testing.T) {
	events := newMockEvents()
	config := &Config{}

	tracker := newTrackerWithStopwatch(
		events, newRunID(), "key", "variationKey", 5, ldcontext.New("key"), config, nil, mockStopwatch(42*time.Millisecond), "")

	expectedResponse := ProviderResponse{
		Usage: TokenUsage{
			Total: 1,
		},
	}

	r, err := tracker.TrackRequest(func(c *Config) (ProviderResponse, error) {
		return expectedResponse, nil
	})

	assert.NoError(t, err)
	assert.Equal(t, expectedResponse, r)

	require.Equal(t, 3, len(events.events))
	gotEvent := events.events[1]
	assert.Equal(t, "$ld:ai:duration:total", gotEvent.name)
	assert.Equal(t, 42.0, gotEvent.metricValue)
}

func TestTracker_TrackDuration(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 6, ldcontext.New("key"), config, nil, "")

	assert.NoError(t, tracker.TrackDuration(time.Millisecond*10))

	runId := extractRunId(t, events)
	expectedEvent := trackEvent{
		name:        "$ld:ai:duration:total",
		context:     ldcontext.New("key"),
		metricValue: 10.0,
		data:        makeTrackData("key", "variationKey", 6, config, runId, ""),
	}

	assert.ElementsMatch(t, []trackEvent{expectedEvent}, events.events)
}

func TestTracker_TrackFeedback(t *testing.T) {
	t.Run("positive feedback", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 7, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackFeedback(FeedbackPositive))

		runId := extractRunId(t, events)
		expectedEvent := trackEvent{
			name:        "$ld:ai:feedback:user:positive",
			context:     ldcontext.New("key"),
			metricValue: 1.0,
			data:        makeTrackData("key", "variationKey", 7, config, runId, ""),
		}

		assert.ElementsMatch(t, []trackEvent{expectedEvent}, events.events)
	})

	t.Run("negative feedback", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 7, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackFeedback(FeedbackNegative))

		runId := extractRunId(t, events)
		expectedEvent := trackEvent{
			name:        "$ld:ai:feedback:user:negative",
			context:     ldcontext.New("key"),
			metricValue: 1.0,
			data:        makeTrackData("key", "variationKey", 7, config, runId, ""),
		}

		assert.ElementsMatch(t, []trackEvent{expectedEvent}, events.events)
	})

	t.Run("invalid feedback returns error", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 7, ldcontext.New("key"), config, nil, "")

		assert.Error(t, tracker.TrackFeedback("not a valid feedback value"))
		assert.Empty(t, events.events)
	})
}

func TestTracker_TrackTokens(t *testing.T) {
	t.Run("only one field set, only one event", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 8, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackTokens(TokenUsage{
			Total: 42,
		}))

		runId := extractRunId(t, events)
		expectedEvent := trackEvent{
			name:        "$ld:ai:tokens:total",
			context:     ldcontext.New("key"),
			metricValue: 42.0,
			data:        makeTrackData("key", "variationKey", 8, config, runId, ""),
		}

		assert.ElementsMatch(t, []trackEvent{expectedEvent}, events.events)
	})

	t.Run("all fields set, all events", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 9, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackTokens(TokenUsage{
			Total:  42,
			Input:  20,
			Output: 22,
		}))

		runId := extractRunId(t, events)
		expectedTotal := trackEvent{
			name:        "$ld:ai:tokens:total",
			context:     ldcontext.New("key"),
			metricValue: 42.0,
			data:        makeTrackData("key", "variationKey", 9, config, runId, ""),
		}

		expectedInput := trackEvent{
			name:        "$ld:ai:tokens:input",
			context:     ldcontext.New("key"),
			metricValue: 20.0,
			data:        makeTrackData("key", "variationKey", 9, config, runId, ""),
		}

		expectedOutput := trackEvent{
			name:        "$ld:ai:tokens:output",
			context:     ldcontext.New("key"),
			metricValue: 22.0,
			data:        makeTrackData("key", "variationKey", 9, config, runId, ""),
		}

		assert.ElementsMatch(t, []trackEvent{expectedTotal, expectedInput, expectedOutput}, events.events)
	})
}

func TestTracker_GetSummary(t *testing.T) {
	t.Run("empty summary when nothing tracked", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 10, ldcontext.New("key"), &Config{}, nil, "")

		summary := tracker.GetSummary()

		assert.True(t, summary.Duration.IsNone())
		assert.True(t, summary.Feedback.IsNone())
		assert.True(t, summary.Tokens.IsNone())
		assert.True(t, summary.Success.IsNone())
		assert.True(t, summary.TimeToFirstToken.IsNone())
	})

	t.Run("first duration is returned", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 11, ldcontext.New("key"), &Config{}, events.log.Loggers, "")

		_ = tracker.TrackDuration(time.Millisecond * 10)
		_ = tracker.TrackDuration(time.Millisecond * 20)

		summary := tracker.GetSummary()

		assert.True(t, summary.Duration.IsSome())
		assert.Equal(t, time.Millisecond*10, summary.Duration.Unwrap())
	})

	t.Run("first feedback is returned", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 12, ldcontext.New("key"), &Config{}, events.log.Loggers, "")

		_ = tracker.TrackFeedback(FeedbackPositive)
		_ = tracker.TrackFeedback(FeedbackNegative)

		summary := tracker.GetSummary()

		assert.True(t, summary.Feedback.IsSome())
		assert.Equal(t, FeedbackPositive, summary.Feedback.Unwrap())
	})

	t.Run("success status tracked correctly", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 13, ldcontext.New("key"), &Config{}, nil, "")

		_ = tracker.TrackSuccess()

		summary := tracker.GetSummary()

		assert.True(t, summary.Success.IsSome())
		assert.True(t, summary.Success.Unwrap())
	})

	t.Run("time to first token is returned", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 14, ldcontext.New("key"), &Config{}, nil, "")

		duration := time.Millisecond * 30
		_ = tracker.TrackTimeToFirstToken(duration)

		summary := tracker.GetSummary()

		assert.True(t, summary.TimeToFirstToken.IsSome())
		assert.Equal(t, duration, summary.TimeToFirstToken.Unwrap())
	})

	t.Run("token usage is returned", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 15, ldcontext.New("key"), &Config{}, nil, "")

		usage := TokenUsage{
			Total:  100,
			Input:  40,
			Output: 60,
		}
		_ = tracker.TrackTokens(usage)

		summary := tracker.GetSummary()

		assert.True(t, summary.Tokens.IsSome())
		assert.Equal(t, usage, summary.Tokens.Unwrap())
	})
}

func TestTracker_RunIdPresentInTrackData(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")
	_ = tracker.TrackSuccess()

	require.NotEmpty(t, events.events)
	data := events.events[0].data
	runId := data.GetByKey("runId").StringValue()
	assert.NotEmpty(t, runId, "runId should be present and non-empty in track data")
}

func TestTracker_AtMostOnce(t *testing.T) {
	t.Run("TrackDuration only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackDuration(10*time.Millisecond))
		assert.NoError(t, tracker.TrackDuration(20*time.Millisecond))

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:duration:total" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackDuration should only emit one event")
	})

	t.Run("TrackTimeToFirstToken only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackTimeToFirstToken(10*time.Millisecond))
		assert.NoError(t, tracker.TrackTimeToFirstToken(20*time.Millisecond))

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:tokens:ttf" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackTimeToFirstToken should only emit one event")
	})

	t.Run("TrackTokens only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackTokens(TokenUsage{Total: 10}))
		assert.NoError(t, tracker.TrackTokens(TokenUsage{Total: 20}))

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:tokens:total" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackTokens should only emit one event")
	})

	t.Run("TrackFeedback only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackFeedback(FeedbackPositive))
		assert.NoError(t, tracker.TrackFeedback(FeedbackNegative))

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:feedback:user:positive" || e.name == "$ld:ai:feedback:user:negative" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackFeedback should only emit one event")
	})

	t.Run("TrackSuccess only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackSuccess())
		assert.NoError(t, tracker.TrackSuccess())

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:generation:success" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackSuccess should only emit one event")
	})

	t.Run("TrackError only tracks once", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackError())
		assert.NoError(t, tracker.TrackError())

		count := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:generation:error" {
				count++
			}
		}
		assert.Equal(t, 1, count, "TrackError should only emit one event")
	})

	t.Run("TrackSuccess then TrackError only tracks success", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, events.log.Loggers, "")

		assert.NoError(t, tracker.TrackSuccess())
		assert.NoError(t, tracker.TrackError())

		assert.Equal(t, 1, len(events.events))
		assert.Equal(t, "$ld:ai:generation:success", events.events[0].name)
	})
}

func TestTracker_TrackJudgeResponse(t *testing.T) {
	t.Run("emits one event per eval score with judgeConfigKey merged into track data", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackJudgeResponse(datamodel.JudgeResponse{
			Success:        true,
			JudgeConfigKey: "my-judge",
			Evals: map[string]datamodel.EvalScore{
				"$ld:ai:judge:relevance": {Score: 0.85, Reasoning: "relevant"},
			},
		}))

		require.Len(t, events.events, 1)
		evt := events.events[0]
		assert.Equal(t, "$ld:ai:judge:relevance", evt.name)
		assert.Equal(t, 0.85, evt.metricValue)
		assert.Equal(t, "my-judge", evt.data.GetByKey("judgeConfigKey").StringValue())

		// All base track data keys must survive the judgeConfigKey merge.
		runId := evt.data.GetByKey("runId").StringValue()
		expectedBase := makeTrackData("key", "variationKey", 1, config, runId, "")
		for _, key := range expectedBase.Keys(nil) {
			assert.Equal(t, expectedBase.GetByKey(key), evt.data.GetByKey(key),
				"track data key %q must be preserved", key)
		}
	})

	t.Run("uses base track data when judgeConfigKey is empty", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackJudgeResponse(datamodel.JudgeResponse{
			Success: true,
			Evals: map[string]datamodel.EvalScore{
				"$ld:ai:judge:relevance": {Score: 0.5},
			},
		}))

		require.Len(t, events.events, 1)
		runId := extractRunId(t, events)
		assert.Equal(t, makeTrackData("key", "variationKey", 1, config, runId, ""), events.events[0].data)
	})

	t.Run("unsuccessful response emits nothing", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")

		assert.NoError(t, tracker.TrackJudgeResponse(datamodel.JudgeResponse{
			Success: false,
			Evals: map[string]datamodel.EvalScore{
				"$ld:ai:judge:relevance": {Score: 0.5},
			},
		}))
		assert.Empty(t, events.events)
	})

	t.Run("may be called multiple times", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")

		response := datamodel.JudgeResponse{
			Success: true,
			Evals: map[string]datamodel.EvalScore{
				"$ld:ai:judge:relevance": {Score: 0.5},
			},
		}
		assert.NoError(t, tracker.TrackJudgeResponse(response))
		assert.NoError(t, tracker.TrackJudgeResponse(response))
		assert.Len(t, events.events, 2)
	})
}

func TestTracker_ResumptionToken(t *testing.T) {
	t.Run("produces valid base64url-encoded token", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "my-config", "var-1", 3, ldcontext.New("key"), config, nil, "")

		token := tracker.ResumptionToken()
		assert.NotEmpty(t, token)

		// Decode and verify
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)

		var payload struct {
			RunID        string `json:"runId"`
			ConfigKey    string `json:"configKey"`
			VariationKey string `json:"variationKey"`
			Version      int    `json:"version"`
		}
		require.NoError(t, json.Unmarshal(decoded, &payload))

		assert.NotEmpty(t, payload.RunID)
		assert.Equal(t, "my-config", payload.ConfigKey)
		assert.Equal(t, "var-1", payload.VariationKey)
		assert.Equal(t, 3, payload.Version)
	})

	t.Run("does not include modelName or providerName", func(t *testing.T) {
		events := newMockEvents()
		config := AICompletionConfig{aiConfigBase: aiConfigBase{model: ModelConfig{Name: "gpt-4"}, provider: ProviderConfig{Name: "openai"}}}
		tracker := newTracker(events, newRunID(), "key", "var", 1, ldcontext.New("key"), &config, nil, "")

		token := tracker.ResumptionToken()
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)

		var raw map[string]interface{}
		require.NoError(t, json.Unmarshal(decoded, &raw))

		_, hasModel := raw["modelName"]
		_, hasProvider := raw["providerName"]
		assert.False(t, hasModel, "token should not contain modelName")
		assert.False(t, hasProvider, "token should not contain providerName")
	})

	t.Run("includes graphKey when set", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "my-config", "var-1", 3, ldcontext.New("key"), config, nil, "my-graph")

		token := tracker.ResumptionToken()
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)

		var payload struct {
			RunID        string `json:"runId"`
			ConfigKey    string `json:"configKey"`
			VariationKey string `json:"variationKey"`
			Version      int    `json:"version"`
			GraphKey     string `json:"graphKey"`
		}
		require.NoError(t, json.Unmarshal(decoded, &payload))

		assert.NotEmpty(t, payload.RunID)
		assert.Equal(t, "my-config", payload.ConfigKey)
		assert.Equal(t, "var-1", payload.VariationKey)
		assert.Equal(t, 3, payload.Version)
		assert.Equal(t, "my-graph", payload.GraphKey)
	})

	t.Run("omits graphKey when unset", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "var", 1, ldcontext.New("key"), config, nil, "")

		token := tracker.ResumptionToken()
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		require.NoError(t, err)

		var raw map[string]interface{}
		require.NoError(t, json.Unmarshal(decoded, &raw))

		_, hasGraphKey := raw["graphKey"]
		assert.False(t, hasGraphKey, "token should not contain graphKey when unset")
	})
}

func TestTracker_GraphKey_InTrackData(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "my-graph")

	assert.NoError(t, tracker.TrackSuccess())

	require.NotEmpty(t, events.events)
	assert.Equal(t, "my-graph", events.events[0].data.GetByKey("graphKey").StringValue())
}

func TestTracker_GraphKey_AbsentWhenEmpty(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

	assert.NoError(t, tracker.TrackSuccess())

	require.NotEmpty(t, events.events)
	data := events.events[0].data
	for _, key := range data.Keys(nil) {
		assert.NotEqual(t, "graphKey", key, "graphKey should be absent from trackData when unset")
	}
}

func TestTrackerFromResumptionToken_GraphKey(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	original := newTracker(events, newRunID(), "my-config", "var-1", 5, ldcontext.New("key"), config, nil, "my-graph")

	token := original.ResumptionToken()
	sdk := newMockSDK(nil, nil)
	reconstructed, err := TrackerFromResumptionToken(token, sdk, ldcontext.New("other-user"))
	require.NoError(t, err)
	require.NotNil(t, reconstructed)

	assert.Equal(t, token, reconstructed.ResumptionToken())

	assert.NoError(t, reconstructed.TrackSuccess())

	require.NotEmpty(t, sdk.events)
	assert.Equal(t, "my-graph", sdk.events[0].data.GetByKey("graphKey").StringValue())
}

func TestTracker_TrackToolCall(t *testing.T) {
	t.Run("emits tool_call event with toolKey in data and metric value 1", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackToolCall("search"))

		require.Len(t, events.events, 1)
		evt := events.events[0]
		assert.Equal(t, "$ld:ai:tool_call", evt.name)
		assert.Equal(t, 1.0, evt.metricValue)
		assert.Equal(t, "search", evt.data.GetByKey("toolKey").StringValue())
	})

	t.Run("may be called multiple times", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackToolCall("search"))
		assert.NoError(t, tracker.TrackToolCall("calculator"))

		assert.Len(t, events.events, 2)
		assert.Equal(t, "$ld:ai:tool_call", events.events[0].name)
		assert.Equal(t, "$ld:ai:tool_call", events.events[1].name)
		assert.Equal(t, "search", events.events[0].data.GetByKey("toolKey").StringValue())
		assert.Equal(t, "calculator", events.events[1].data.GetByKey("toolKey").StringValue())
	})

	t.Run("base trackData keys are preserved in tool call event", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackToolCall("search"))

		runId := extractRunId(t, events)
		expectedBase := makeTrackData("key", "variationKey", 1, config, runId, "")
		for _, key := range expectedBase.Keys(nil) {
			assert.Equal(t, expectedBase.GetByKey(key), events.events[0].data.GetByKey(key),
				"track data key %q must be preserved", key)
		}
	})
}

func TestTracker_TrackToolCalls(t *testing.T) {
	t.Run("emits one event per tool key", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		keys := []string{"search", "calculator", "weather"}
		assert.NoError(t, tracker.TrackToolCalls(keys))

		require.Len(t, events.events, 3)
		for i, key := range keys {
			assert.Equal(t, "$ld:ai:tool_call", events.events[i].name)
			assert.Equal(t, key, events.events[i].data.GetByKey("toolKey").StringValue())
		}
	})

	t.Run("empty slice emits no events", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

		assert.NoError(t, tracker.TrackToolCalls([]string{}))
		assert.Empty(t, events.events)
	})
}

func TestTracker_GetSummary_ToolCalls(t *testing.T) {
	t.Run("empty when no tool calls tracked", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")

		assert.Empty(t, tracker.GetSummary().ToolCalls)
	})

	t.Run("reflects keys passed to TrackToolCall in order", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")

		_ = tracker.TrackToolCall("search")
		_ = tracker.TrackToolCall("calculator")
		_ = tracker.TrackToolCall("search")

		assert.Equal(t, []string{"search", "calculator", "search"}, tracker.GetSummary().ToolCalls)
	})

	t.Run("summary ToolCalls is a defensive copy", func(t *testing.T) {
		events := newMockEvents()
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), &Config{}, nil, "")

		_ = tracker.TrackToolCall("search")
		summary := tracker.GetSummary()
		summary.ToolCalls[0] = "mutated"

		assert.Equal(t, []string{"search"}, tracker.GetSummary().ToolCalls)
	})
}

func TestTracker_GetSummary_ResumptionToken(t *testing.T) {
	events := newMockEvents()
	config := &Config{}
	tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), config, nil, "")

	summary := tracker.GetSummary()

	assert.NotEmpty(t, summary.ResumptionToken)
	assert.Equal(t, tracker.ResumptionToken(), summary.ResumptionToken)
}

func TestTracker_GetTrackData(t *testing.T) {
	t.Run("all fields populated from constructor args", func(t *testing.T) {
		events := newMockEvents()
		config := AICompletionConfig{aiConfigBase: aiConfigBase{model: ModelConfig{Name: "gpt-4"}, provider: ProviderConfig{Name: "openai"}}}
		tracker := newTracker(events, "fixed-run-id", "my-config", "var-1", 3, ldcontext.New("key"), &config, nil, "my-graph")

		td := tracker.GetTrackData()

		assert.Equal(t, "fixed-run-id", td.RunID)
		assert.Equal(t, "my-config", td.ConfigKey)
		assert.Equal(t, 3, td.Version)
		assert.Equal(t, "var-1", td.VariationKey)
		assert.Equal(t, "gpt-4", td.ModelName)
		assert.Equal(t, "openai", td.ProviderName)
		assert.Equal(t, "my-graph", td.GraphKey)
		assert.Equal(t, SDKName, td.AISdkName)
		assert.Equal(t, Version, td.AISdkVersion)
	})

	t.Run("empty optional fields when not set", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTracker(events, newRunID(), "key", "", 1, ldcontext.New("key"), config, nil, "")

		td := tracker.GetTrackData()

		assert.Empty(t, td.VariationKey)
		assert.Empty(t, td.GraphKey)
	})
}

func TestExplicitVersionZeroInResumptionToken(t *testing.T) {
	// A tracker with version 0 must encode and decode 0, not 1.
	events := newMockEvents()
	config := AICompletionConfig{}
	tracker := newTracker(events, newRunID(), "key", "var", 0, ldcontext.New("user"), &config, nil, "")

	token := tracker.ResumptionToken()
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)

	var payload struct {
		Version int `json:"version"`
	}
	require.NoError(t, json.Unmarshal(decoded, &payload))
	assert.Equal(t, 0, payload.Version)
}

func ptrFloat64(v float64) *float64 { return &v }

func TestTrackDurationOf(t *testing.T) {
	t.Run("tracks measured duration and returns nil error", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(55*time.Millisecond), "")

		err := tracker.TrackDurationOf(func() error { return nil })

		assert.NoError(t, err)
		require.Len(t, events.events, 1)
		assert.Equal(t, "$ld:ai:duration:total", events.events[0].name)
		assert.Equal(t, 55.0, events.events[0].metricValue)
	})

	t.Run("returns operation error and still tracks duration", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(10*time.Millisecond), "")

		opErr := fmt.Errorf("boom")
		err := tracker.TrackDurationOf(func() error { return opErr })

		assert.Equal(t, opErr, err)
		require.Len(t, events.events, 1)
		assert.Equal(t, "$ld:ai:duration:total", events.events[0].name)
	})
}

func TestTrackMetricsOf(t *testing.T) {
	t.Run("completion tracker: tracks duration, success, and tokens", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(20*time.Millisecond), "")

		type result struct{ val string }
		tokens := TokenUsage{Total: 10, Input: 4, Output: 6}

		got, err := TrackMetricsOf(tracker,
			func(r result) AIMetrics {
				return AIMetrics{Success: true, Tokens: &tokens}
			},
			func() (result, error) { return result{"ok"}, nil },
		)

		assert.NoError(t, err)
		assert.Equal(t, result{"ok"}, got)

		names := make([]string, len(events.events))
		for i, e := range events.events {
			names[i] = e.name
		}
		assert.Contains(t, names, "$ld:ai:generation:success")
		assert.Contains(t, names, "$ld:ai:duration:total")
		assert.Contains(t, names, "$ld:ai:tokens:total")

		for _, e := range events.events {
			if e.name == "$ld:ai:duration:total" {
				assert.Equal(t, 20.0, e.metricValue)
			}
		}
	})

	t.Run("judge tracker: works without completion-only rejection", func(t *testing.T) {
		events := newMockEvents()
		judgeConfig := &AIJudgeConfig{}
		tracker := newTracker(events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"), judgeConfig, nil, "")

		_, err := TrackMetricsOf(tracker,
			func(_ struct{}) AIMetrics { return AIMetrics{} },
			func() (struct{}, error) { return struct{}{}, nil },
		)

		assert.NoError(t, err)
	})

	t.Run("DurationMs override used instead of wall-clock", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(99*time.Millisecond), "")

		_, err := TrackMetricsOf(tracker,
			func(_ struct{}) AIMetrics {
				return AIMetrics{DurationMs: ptrFloat64(100.0)}
			},
			func() (struct{}, error) { return struct{}{}, nil },
		)

		assert.NoError(t, err)
		for _, e := range events.events {
			if e.name == "$ld:ai:duration:total" {
				assert.Equal(t, 100.0, e.metricValue, "runner-reported duration should be used")
				return
			}
		}
		t.Fatal("expected a duration event")
	})

	t.Run("error path: tracks error, no success or tokens", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(5*time.Millisecond), "")

		opErr := fmt.Errorf("model failed")
		tokens := TokenUsage{Total: 5}

		_, err := TrackMetricsOf(tracker,
			func(_ struct{}) AIMetrics {
				return AIMetrics{Tokens: &tokens}
			},
			func() (struct{}, error) { return struct{}{}, opErr },
		)

		assert.Equal(t, opErr, err)

		for _, e := range events.events {
			assert.NotEqual(t, "$ld:ai:generation:success", e.name)
			assert.NotEqual(t, "$ld:ai:tokens:total", e.name)
		}

		errorEvents := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:generation:error" {
				errorEvents++
			}
		}
		assert.Equal(t, 1, errorEvents, "expected exactly one error event")
	})

	t.Run("Success:false with nil error emits generation:error not generation:success", func(t *testing.T) {
		events := newMockEvents()
		config := &Config{}
		tracker := newTrackerWithStopwatch(
			events, newRunID(), "key", "variationKey", 1, ldcontext.New("key"),
			config, nil, mockStopwatch(10*time.Millisecond), "")

		got, err := TrackMetricsOf(tracker,
			func(_ struct{}) AIMetrics { return AIMetrics{Success: false} },
			func() (struct{}, error) { return struct{}{}, nil },
		)

		assert.NoError(t, err)
		assert.Equal(t, struct{}{}, got)

		for _, e := range events.events {
			assert.NotEqual(t, "$ld:ai:generation:success", e.name, "must not emit success when Success is false")
		}

		errorEvents := 0
		for _, e := range events.events {
			if e.name == "$ld:ai:generation:error" {
				errorEvents++
			}
		}
		assert.Equal(t, 1, errorEvents, "expected exactly one error event when Success is false")
	})
}
