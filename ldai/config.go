package ldai

import (
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
)

// Config is an AI Config returned from the LaunchDarkly client.
//
// Deprecated: Use AICompletionConfig, AIAgentConfig, or AIJudgeConfig instead.
type Config = AICompletionConfig

// Disabled returns an AICompletionConfigDefault that is disabled and contains no messages.
// It is a convenience constructor equivalent to NewAICompletionConfigDefault().Disabled().
//
// Deprecated: Use NewAICompletionConfigDefault().Disabled() instead.
func Disabled() AICompletionConfigDefault {
	return NewAICompletionConfigDefault().Disabled()
}

// Config is a deprecated alias for Client.CompletionConfig. Use CompletionConfig instead.
//
// Deprecated: Use Client.CompletionConfig instead.
func (c *Client) Config(
	key string,
	context ldcontext.Context,
	defaultValue AICompletionConfigDefault,
	variables map[string]interface{},
) Config {
	return c.CompletionConfig(key, context, defaultValue, variables)
}
