package ldai

import (
	"slices"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// AIJudgeConfig represents a judge-mode AI Config retrieved from LaunchDarkly.
// It provides access to model parameters, provider details, judge messages, and the
// evaluation metric key. Use Client.JudgeConfig (updated in Task 03) to obtain an instance.
//
// To send analytic events to LaunchDarkly, call CreateTracker to obtain a Tracker.
type AIJudgeConfig struct {
	aiConfigBase
	messages            []datamodel.Message
	evaluationMetricKey string
}

// Messages returns the interpolated judge messages. The messages may contain placeholder
// strings for the message history and response to evaluate, resolved during evaluation.
func (c *AIJudgeConfig) Messages() []datamodel.Message {
	return slices.Clone(c.messages)
}

// EvaluationMetricKey returns the evaluation metric key used to record judge scores.
func (c *AIJudgeConfig) EvaluationMetricKey() string {
	return c.evaluationMetricKey
}
