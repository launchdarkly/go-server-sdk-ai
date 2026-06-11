package judge

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfig struct {
	disabled             bool
	messages             []datamodel.Message
	modelParam           map[string]ldvalue.Value
	customParam          map[string]ldvalue.Value
	evaluationMetricKey  string
	evaluationMetricKeys []string
}

func (m *mockConfig) Enabled() bool {
	return !m.disabled
}

func (m *mockConfig) Messages() []datamodel.Message {
	return m.messages
}

func (m *mockConfig) ModelParam(key string) (ldvalue.Value, bool) {
	val, ok := m.modelParam[key]
	return val, ok
}

func (m *mockConfig) CustomModelParam(key string) (ldvalue.Value, bool) {
	val, ok := m.customParam[key]
	return val, ok
}

func (m *mockConfig) EvaluationMetricKey() string {
	return m.evaluationMetricKey
}

func (m *mockConfig) EvaluationMetricKeys() []string {
	return m.evaluationMetricKeys
}

type mockTracker struct {
	judgeResponses []datamodel.JudgeResponse
	usages         []ldai.TokenUsage
}

func (m *mockTracker) TrackJudgeResponse(response datamodel.JudgeResponse) error {
	m.judgeResponses = append(m.judgeResponses, response)
	return nil
}

func (m *mockTracker) TrackTokens(usage ldai.TokenUsage) error {
	m.usages = append(m.usages, usage)
	return nil
}

type mockProvider struct {
	response StructuredResponse
	err      error
	calls    [][]datamodel.Message
}

func (m *mockProvider) InvokeStructuredModel(messages []datamodel.Message, schema map[string]interface{}) (StructuredResponse, error) {
	m.calls = append(m.calls, messages)
	return m.response, m.err
}

// evaluationContent builds a structured response body in the top-level {score, reasoning} shape.
func evaluationContent(score interface{}, reasoning interface{}) map[string]interface{} {
	return map[string]interface{}{
		"score":     score,
		"reasoning": reasoning,
	}
}

func TestNew(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
	}
	tracker := &mockTracker{}
	provider := &mockProvider{}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)
	assert.NotNil(t, judge)
	assert.Equal(t, "$ld:ai:judge:relevance", judge.metricKey)
	assert.Equal(t, "test-judge", judge.judgeConfigKey)
}

func TestNew_MissingMetricKey(t *testing.T) {
	config := &mockConfig{}
	tracker := &mockTracker{}
	provider := &mockProvider{}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	assert.Error(t, err)
	assert.Nil(t, judge)
	assert.Contains(t, err.Error(), "missing evaluationMetricKey")
}

func TestNew_DisabledConfig(t *testing.T) {
	config := &mockConfig{
		disabled:            true,
		evaluationMetricKey: "$ld:ai:judge:relevance",
	}

	judge, err := New(config, &mockTracker{}, &mockProvider{}, "test-judge", nil)
	assert.Error(t, err)
	assert.Nil(t, judge)
	assert.Contains(t, err.Error(), "disabled")
}

func TestNew_NilInputs(t *testing.T) {
	config := &mockConfig{evaluationMetricKey: "test"}
	tracker := &mockTracker{}
	provider := &mockProvider{}

	_, err := New(nil, tracker, provider, "test", nil)
	assert.Error(t, err)

	_, err = New(config, nil, provider, "test", nil)
	assert.Error(t, err)

	_, err = New(config, tracker, nil, "test", nil)
	assert.Error(t, err)
}

func TestEvaluate_Success(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages: []datamodel.Message{
			{Role: datamodel.System, Content: "Evaluate this"},
		},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{
			Content: evaluationContent(0.85, "Highly relevant"),
			Usage:   ldai.TokenUsage{Total: 100, Input: 60, Output: 40},
		},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("test input", "test output", 1.0)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "test-judge", result.JudgeConfigKey)
	assert.Len(t, result.Evals, 1)
	assert.Equal(t, 0.85, result.Evals["$ld:ai:judge:relevance"].Score)
	assert.Equal(t, "Highly relevant", result.Evals["$ld:ai:judge:relevance"].Reasoning)

	assert.Len(t, tracker.usages, 1)
	assert.Equal(t, 100, tracker.usages[0].Total)
	// Note: Judge should NOT track responses internally - this is caller's responsibility
	// The judge's tracker is only used for usage/duration metrics
	assert.Len(t, tracker.judgeResponses, 0, "Judge should not track responses internally")

	// The provider receives the config messages followed by the evaluation input.
	require.Len(t, provider.calls, 1)
	require.Len(t, provider.calls[0], 2)
	assert.Equal(t, "Evaluate this", provider.calls[0][0].Content)
	assert.Equal(t, datamodel.User, provider.calls[0][1].Role)
	assert.Equal(t, "MESSAGE HISTORY:\ntest input\n\nRESPONSE TO EVALUATE:\ntest output",
		provider.calls[0][1].Content)
}

// TestEvaluate_StripsLegacyMessages verifies that legacy judge template messages (containing the
// reserved placeholders) are removed before invoking the provider; only system messages and
// placeholder-free messages survive, followed by the evaluation input.
func TestEvaluate_StripsLegacyMessages(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:test",
		messages: []datamodel.Message{
			{Role: datamodel.System, Content: "You are a judge"},
			{Role: datamodel.User, Content: "Input: {{message_history}}"},
			{Role: datamodel.User, Content: "Output: {{response_to_evaluate}}"},
		},
	}

	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(0.9, "Good response")},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("What is AI?", "AI is artificial intelligence", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, provider.calls, 1)
	messages := provider.calls[0]
	require.Len(t, messages, 2, "legacy placeholder messages must be stripped")

	assert.Equal(t, "You are a judge", messages[0].Content)
	assert.Equal(t, "MESSAGE HISTORY:\nWhat is AI?\n\nRESPONSE TO EVALUATE:\nAI is artificial intelligence",
		messages[1].Content)
}

func TestEvaluate_NonFloat64Scores(t *testing.T) {
	cases := []struct {
		name     string
		score    interface{}
		expected float64
	}{
		{"integer score", 1, 1.0},
		{"int64 score", int64(0), 0.0},
		{"float32 score", float32(0.5), 0.5},
		{"json.Number score", json.Number("0.5"), 0.5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config := &mockConfig{
				evaluationMetricKey: "$ld:ai:judge:relevance",
				messages: []datamodel.Message{
					{Role: datamodel.System, Content: "Evaluate this"},
				},
			}
			provider := &mockProvider{
				response: StructuredResponse{Content: evaluationContent(c.score, "ok")},
			}

			judge, err := New(config, &mockTracker{}, provider, "test-judge", nil)
			require.NoError(t, err)

			result, err := judge.Evaluate("input", "output", 1.0)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.Success)
			assert.Equal(t, c.expected, result.Evals["$ld:ai:judge:relevance"].Score)
		})
	}
}

func TestEvaluate_InvalidJSONNumberScore(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages: []datamodel.Message{
			{Role: datamodel.System, Content: "Evaluate this"},
		},
	}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(json.Number("abc"), "ok")},
	}

	judge, err := New(config, &mockTracker{}, provider, "test-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "invalid score")
}

// TestEvaluate_EmptyConfigMessages verifies that a config without messages still evaluates: the
// evaluation input alone is sent to the provider. (Matches the Python/Node SDKs, which no longer
// require judge config messages.)
func TestEvaluate_EmptyConfigMessages(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(0.5, "ok")},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)

	require.Len(t, provider.calls, 1)
	require.Len(t, provider.calls[0], 1)
	assert.Equal(t, "MESSAGE HISTORY:\ninput\n\nRESPONSE TO EVALUATE:\noutput",
		provider.calls[0][0].Content)
}

func TestEvaluate_Sampling(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	sampled := 0
	for i := 0; i < 100; i++ {
		result, _ := judge.Evaluate("input", "output", 0.0)
		if result != nil {
			sampled++
		}
	}
	assert.Equal(t, 0, sampled)
}

func TestEvaluate_ProviderError(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{err: fmt.Errorf("provider error")}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("input", "output", 1.0)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "test-judge", result.JudgeConfigKey)
	assert.Contains(t, result.Error, "provider error")
}

func TestEvaluate_InvalidResponse(t *testing.T) {
	tests := []struct {
		name          string
		response      map[string]interface{}
		expectedError string
	}{
		{
			name:          "empty response",
			response:      map[string]interface{}{},
			expectedError: "invalid score",
		},
		{
			name:          "missing score",
			response:      map[string]interface{}{"reasoning": "test"},
			expectedError: "invalid score",
		},
		{
			name:          "null score",
			response:      evaluationContent(nil, "test"),
			expectedError: "invalid score",
		},
		{
			name:          "invalid score type",
			response:      evaluationContent("not a number", "test"),
			expectedError: "invalid score",
		},
		{
			name:          "score as numeric string",
			response:      evaluationContent("0.5", "test"),
			expectedError: "invalid score",
		},
		{
			name:          "score greater than one",
			response:      evaluationContent(1.5, "test"),
			expectedError: "invalid score",
		},
		{
			name:          "negative score",
			response:      evaluationContent(-0.5, "test"),
			expectedError: "invalid score",
		},
		{
			name:          "NaN score",
			response:      evaluationContent(math.NaN(), "test"),
			expectedError: "invalid score",
		},
		{
			name:          "infinite score",
			response:      evaluationContent(math.Inf(1), "test"),
			expectedError: "invalid score",
		},
		{
			name:          "invalid reasoning type",
			response:      evaluationContent(0.5, 123),
			expectedError: "invalid reasoning",
		},
		{
			name:          "missing reasoning",
			response:      map[string]interface{}{"score": 0.5},
			expectedError: "invalid reasoning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &mockConfig{
				evaluationMetricKey: "$ld:ai:judge:relevance",
				messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
			}
			tracker := &mockTracker{}
			provider := &mockProvider{
				response: StructuredResponse{Content: tt.response},
			}

			judge, err := New(config, tracker, provider, "test-judge", nil)
			require.NoError(t, err)

			result, err := judge.Evaluate("input", "output", 1.0)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.False(t, result.Success)
			assert.Contains(t, result.Error, tt.expectedError)
		})
	}
}

func TestEvaluateMessages(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.System, Content: "You are a judge"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(0.9, "Excellent")},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	messages := []datamodel.Message{
		{Role: datamodel.User, Content: "Hello"},
		{Role: datamodel.Assistant, Content: "Hi there"},
	}

	result, err := judge.EvaluateMessages(messages, "response", 1.0)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)

	// History is rendered as "<role>: <content>" lines so the judge can distinguish speakers.
	require.Len(t, provider.calls, 1)
	require.Len(t, provider.calls[0], 2)
	assert.Equal(t, "MESSAGE HISTORY:\nuser: Hello\nassistant: Hi there\n\nRESPONSE TO EVALUATE:\nresponse",
		provider.calls[0][1].Content)
}

func TestGetMetricKey(t *testing.T) {
	tests := []struct {
		name                 string
		evaluationMetricKey  string
		evaluationMetricKeys []string
		want                 string
		wantErr              bool
	}{
		{
			name:                "from top-level field (primary)",
			evaluationMetricKey: "$ld:ai:judge:toplevel",
			want:                "$ld:ai:judge:toplevel",
		},
		{
			name:                 "top-level field has priority over array",
			evaluationMetricKey:  "$ld:ai:judge:toplevel",
			evaluationMetricKeys: []string{"$ld:ai:judge:array"},
			want:                 "$ld:ai:judge:toplevel",
		},
		{
			name:    "missing",
			wantErr: true,
		},
		{
			name:                "trim whitespace from top-level",
			evaluationMetricKey: "  $ld:ai:judge:toplevel  ",
			want:                "$ld:ai:judge:toplevel",
		},
		{
			name:                 "from evaluationMetricKeys array",
			evaluationMetricKeys: []string{"$ld:ai:judge:relevance", "$ld:ai:judge:accuracy"},
			want:                 "$ld:ai:judge:relevance",
		},
		{
			name:                 "skip empty strings in array",
			evaluationMetricKeys: []string{"", "  ", "$ld:ai:judge:relevance"},
			want:                 "$ld:ai:judge:relevance",
		},
		{
			name:                 "trim whitespace from array entry",
			evaluationMetricKeys: []string{"  $ld:ai:judge:relevance  "},
			want:                 "$ld:ai:judge:relevance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &mockConfig{
				evaluationMetricKey:  tt.evaluationMetricKey,
				evaluationMetricKeys: tt.evaluationMetricKeys,
			}
			got, err := getMetricKey(config, nil, "test-judge")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestBuildSchema verifies the fixed evaluation schema shape: top-level score and reasoning, both
// required, with no metric-key-specific structure.
func TestBuildSchema(t *testing.T) {
	schema := buildSchema()

	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, []string{"score", "reasoning"}, schema["required"])
	assert.Equal(t, false, schema["additionalProperties"])

	props := schema["properties"].(map[string]interface{})
	require.Contains(t, props, "score")
	require.Contains(t, props, "reasoning")

	scoreSchema := props["score"].(map[string]interface{})
	assert.Equal(t, "number", scoreSchema["type"])
	assert.Equal(t, 0.0, scoreSchema["minimum"])
	assert.Equal(t, 1.0, scoreSchema["maximum"])

	reasoningSchema := props["reasoning"].(map[string]interface{})
	assert.Equal(t, "string", reasoningSchema["type"])
}

func TestGetMetricKey_EmptyArray(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKeys: []string{},
	}

	_, err := getMetricKey(config, nil, "test-judge")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing evaluationMetricKey")
}

func TestGetMetricKey_ArrayWithOnlyEmptyStrings(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKeys: []string{"", "  ", "\t"},
	}

	_, err := getMetricKey(config, nil, "test-judge")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing evaluationMetricKey")
}

// TestEvaluate_ResultKeyedByMetricKey verifies that the parsed top-level score/reasoning is keyed
// by the judge config's evaluation metric key, preserving the dynamic eval-score event keys in the
// tracking wire contract.
func TestEvaluate_ResultKeyedByMetricKey(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(0.75, "Good response")},
	}

	judge, err := New(config, tracker, provider, "my-judge-config", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "my-judge-config", result.JudgeConfigKey)
	assert.True(t, result.Success)
	assert.Equal(t, 0.75, result.Evals["$ld:ai:judge:relevance"].Score)
	assert.Equal(t, "Good response", result.Evals["$ld:ai:judge:relevance"].Reasoning)

	// Judge should NOT track responses internally - this is caller's responsibility
	assert.Len(t, tracker.judgeResponses, 0, "Judge should not track responses internally")
}

func TestEvaluate_TokenUsageTracked(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{
			Content: evaluationContent(0.5, "test"),
			Usage:   ldai.TokenUsage{Total: 150, Input: 90, Output: 60},
		},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	_, err = judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)

	require.Len(t, tracker.usages, 1)
	assert.Equal(t, 150, tracker.usages[0].Total)
	assert.Equal(t, 90, tracker.usages[0].Input)
	assert.Equal(t, 60, tracker.usages[0].Output)
}

func TestEvaluate_NoTokenUsageWhenZero(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{
		response: StructuredResponse{
			Content: evaluationContent(0.5, "test"),
			Usage:   ldai.TokenUsage{},
		},
	}

	judge, err := New(config, tracker, provider, "test-judge", nil)
	require.NoError(t, err)

	_, err = judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)

	assert.Len(t, tracker.usages, 0)
}

func TestEvaluate_ErrorResponseIncludesJudgeConfigKey(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages:            []datamodel.Message{{Role: datamodel.User, Content: "test"}},
	}
	tracker := &mockTracker{}
	provider := &mockProvider{err: fmt.Errorf("test error")}

	judge, err := New(config, tracker, provider, "error-judge", nil)
	require.NoError(t, err)

	result, err := judge.Evaluate("input", "output", 1.0)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "error-judge", result.JudgeConfigKey)
}

// Integration Tests - These verify end-to-end behavior and patterns not caught by unit tests

// TestIntegration_AIConfigTracksJudgeResults simulates the real-world pattern where
// an AI config evaluates with a judge and tracks results on its own tracker.
func TestIntegration_AIConfigTracksJudgeResults(t *testing.T) {
	// Simulate AI config's tracker
	aiConfigTracker := &mockTracker{}

	// Simulate judge's tracker (should NOT be used for judge response tracking)
	judgeTracker := &mockTracker{}

	// Judge configuration (legacy template message will be stripped)
	judgeConfig := &mockConfig{
		evaluationMetricKey: "$ld:ai:judge:relevance",
		messages: []datamodel.Message{
			{Role: datamodel.User, Content: "Evaluate: {{message_history}} -> {{response_to_evaluate}}"},
		},
	}

	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(0.85, "Highly relevant")},
	}

	// Create judge with its own tracker
	judge, err := New(judgeConfig, judgeTracker, provider, "test-judge", nil)
	require.NoError(t, err)

	// AI config evaluates with the judge
	result, err := judge.Evaluate("What is AI?", "AI is artificial intelligence", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)

	// AI config tracks the result on its own tracker (NOT the judge's tracker)
	err = aiConfigTracker.TrackJudgeResponse(*result)
	require.NoError(t, err)

	// Verify tracking happened on AI config's tracker
	assert.Len(t, aiConfigTracker.judgeResponses, 1, "AI config should track judge response")
	assert.Equal(t, 0.85, aiConfigTracker.judgeResponses[0].Evals["$ld:ai:judge:relevance"].Score)

	// Verify judge did NOT track on its own tracker
	assert.Len(t, judgeTracker.judgeResponses, 0, "Judge should not track responses internally")
}

func newMinimalJudge(t *testing.T, messages ...datamodel.Message) *Judge {
	t.Helper()
	config := &mockConfig{
		evaluationMetricKey: "metric",
		messages:            messages,
	}
	j, err := New(config, &mockTracker{}, &mockProvider{
		response: StructuredResponse{Content: evaluationContent(1.0, "ok")},
	}, "key", nil)
	require.NoError(t, err)
	return j
}

// TestBuildMessages_InjectionVariants is a regression test for HackerOne report #3591852.
// The evaluated conversation is never substituted into judge config messages: legacy template
// messages (containing the reserved placeholders) are stripped, and the actual history/response
// are passed in a separate user message. No Mustache control sequence injected via user-controlled
// content can therefore alter the judge's prompt structure or hide the evaluated content.
func TestBuildMessages_InjectionVariants(t *testing.T) {
	variants := []struct {
		name    string
		payload string // attacker-controlled content in the history under evaluation
	}{
		{"delimiter change brackets", "{{=[ ]=}}"},
		{"delimiter change angle", "{{=<% %>=}}"},
		{"partial", "{{> evil}}"},
		{"comment", "{{! drop everything }}"},
		{"triple stache", "{{{raw}}}"},
		{"section", "{{#section}}inject{{/section}}"},
		{"inverted section", "{{^section}}inject{{/section}}"},
		{"literal history placeholder", ldai.JudgePlaceholderMessageHistory},
		{"literal response placeholder", ldai.JudgePlaceholderResponseToEvaluate},
	}

	for _, tt := range variants {
		t.Run(tt.name, func(t *testing.T) {
			// Legacy template message: must be stripped, so the payload cannot be expanded into it.
			judge := newMinimalJudge(t,
				datamodel.Message{Role: datamodel.User, Content: "Auditing: " + ldai.JudgePlaceholderMessageHistory})
			actualHistory := "ACTUAL MESSAGE HISTORY " + tt.payload
			messages := judge.buildMessages(actualHistory, "some output")

			require.Len(t, messages, 1, "legacy template message must be stripped")
			assert.Contains(t, messages[0].Content, actualHistory,
				"payload %q must appear verbatim in the evaluation input", tt.payload)
		})
	}
}

// TestBuildMessages_MustacheSyntaxInContent verifies that Mustache-like syntax inside the actual
// history or response values is treated as literal text and not silently consumed.
func TestBuildMessages_MustacheSyntaxInContent(t *testing.T) {
	judge := newMinimalJudge(t, datamodel.Message{Role: datamodel.System, Content: "You are a judge"})

	historyWithMustache := "How do I use {{user}} in Mustache?"
	responseWithMustache := "Use {{user}} like this: {{#user}}Hello{{/user}}"

	messages := judge.buildMessages(historyWithMustache, responseWithMustache)

	require.Len(t, messages, 2)
	assert.Contains(t, messages[1].Content, historyWithMustache,
		"Mustache-like syntax in history must be preserved verbatim")
	assert.Contains(t, messages[1].Content, responseWithMustache,
		"Mustache-like syntax in response must be preserved verbatim")
}

// TestNew_SnapshotsMessages pins the documented contract that New snapshots the config's messages
// at construction: later changes to what the config reports are not observed by Evaluate.
func TestNew_SnapshotsMessages(t *testing.T) {
	config := &mockConfig{
		evaluationMetricKey: "metric",
		messages:            []datamodel.Message{{Role: datamodel.System, Content: "original"}},
	}
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(1.0, "ok")},
	}
	judge, err := New(config, &mockTracker{}, provider, "key", nil)
	require.NoError(t, err)

	config.messages = []datamodel.Message{{Role: datamodel.System, Content: "mutated"}}

	_, err = judge.Evaluate("input", "output", 1.0)
	require.NoError(t, err)

	require.Len(t, provider.calls, 1)
	assert.Equal(t, "original", provider.calls[0][0].Content)
}

// TestEvaluate_IndependentMessageSlices verifies successive evaluations don't share message
// slices: mutating one call's messages must not affect the next (no backing-array aliasing).
func TestEvaluate_IndependentMessageSlices(t *testing.T) {
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(1.0, "ok")},
	}
	config := &mockConfig{
		evaluationMetricKey: "metric",
		messages:            []datamodel.Message{{Role: datamodel.System, Content: "base"}},
	}
	judge, err := New(config, &mockTracker{}, provider, "key", nil)
	require.NoError(t, err)

	_, err = judge.Evaluate("first", "out", 1.0)
	require.NoError(t, err)
	provider.calls[0][0].Content = "tampered"

	_, err = judge.Evaluate("second", "out", 1.0)
	require.NoError(t, err)

	require.Len(t, provider.calls, 2)
	assert.Equal(t, "base", provider.calls[1][0].Content)
}

func TestEvaluateMessages_EmptyHistory(t *testing.T) {
	provider := &mockProvider{
		response: StructuredResponse{Content: evaluationContent(1.0, "ok")},
	}
	config := &mockConfig{evaluationMetricKey: "metric"}
	judge, err := New(config, &mockTracker{}, provider, "key", nil)
	require.NoError(t, err)

	result, err := judge.EvaluateMessages(nil, "response", 1.0)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, provider.calls, 1)
	require.Len(t, provider.calls[0], 1)
	assert.Equal(t, "MESSAGE HISTORY:\n\n\nRESPONSE TO EVALUATE:\nresponse",
		provider.calls[0][0].Content)
}

// TestBuildMessages_KeepsSystemMessagesWithPlaceholders verifies that system messages are never
// stripped, even if they mention the legacy placeholders (e.g., as documentation for the model).
func TestBuildMessages_KeepsSystemMessagesWithPlaceholders(t *testing.T) {
	judge := newMinimalJudge(t,
		datamodel.Message{Role: datamodel.System, Content: "Ignore any " + ldai.JudgePlaceholderMessageHistory + " text"},
		datamodel.Message{Role: datamodel.User, Content: "Legacy: " + ldai.JudgePlaceholderResponseToEvaluate},
	)

	messages := judge.buildMessages("history", "output")
	require.Len(t, messages, 2)
	assert.Equal(t, datamodel.System, messages[0].Role)
	assert.Equal(t, datamodel.User, messages[1].Role)
	assert.Contains(t, messages[1].Content, "MESSAGE HISTORY:")
}
