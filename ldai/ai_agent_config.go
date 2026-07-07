package ldai

import (
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// AIAgentConfig represents an agent-mode AI Config retrieved from LaunchDarkly.
// It provides access to model parameters, provider details, agent instructions, tools, and
// judge configuration. Use Client.AgentConfig (added in Task 03) to obtain an instance.
//
// To send analytic events to LaunchDarkly, call CreateTracker to obtain a Tracker.
type AIAgentConfig struct {
	aiConfigBase
	instructions       string
	judgeConfiguration *datamodel.JudgeConfiguration
}

// Instructions returns the agent's system instructions string.
func (c *AIAgentConfig) Instructions() string {
	return c.instructions
}

// JudgeConfiguration returns the judge configuration attached to this config, if any.
// Returns a defensive copy to prevent mutations.
func (c *AIAgentConfig) JudgeConfiguration() *datamodel.JudgeConfiguration {
	return c.judgeConfiguration.Clone()
}
