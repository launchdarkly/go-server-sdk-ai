package providers

import (
	"context"
	"time"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// Metrics contains the metrics for a single AI invocation, as reported by a Runner.
type Metrics struct {
	// Success indicates whether the invocation succeeded.
	Success bool

	// Tokens is the token usage for the invocation. The zero value means usage was not reported;
	// see datamodel.TokenUsage.Set.
	Tokens datamodel.TokenUsage

	// ToolCalls is the ordered list of tool-call names observed during the invocation, or nil if
	// none were observed.
	ToolCalls []string

	// Duration is the wall-clock duration of the invocation, or zero if not measured.
	Duration time.Duration
}

// RunnerResult contains the result of a single AI model invocation.
type RunnerResult struct {
	// Content is the text content returned by the model.
	Content string

	// Metrics contains the metrics for this invocation.
	Metrics Metrics

	// Raw is the provider-native response object, for advanced consumers. May be nil.
	Raw interface{}

	// Parsed is the parsed structured output. It is populated only when an output schema was
	// supplied to Run.
	Parsed map[string]interface{}
}

// Runner is a focused, configured object that performs a single kind of AI invocation. One Runner
// interface covers completion, agent, and judge use cases.
//
// Whether a Runner is safe for concurrent use is implementation-defined; multi-turn runners that
// accumulate conversation history generally are not.
type Runner interface {
	// Run invokes the model with the given input string. outputSchema may be nil for plain text
	// output; when it is a JSON Schema, the model is asked for structured output and the parsed
	// result is available via RunnerResult.Parsed. Implementations must honor ctx cancellation.
	Run(ctx context.Context, input string, outputSchema map[string]interface{}) (RunnerResult, error)
}
