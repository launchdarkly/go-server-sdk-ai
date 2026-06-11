// Package providers defines the pluggable AI provider abstraction for the LaunchDarkly AI SDK.
//
// An AIProvider is a per-provider factory that constructs Runner instances for AI Configs.
// Concrete providers live in nested modules under providers/ (for example
// github.com/launchdarkly/go-server-sdk-ai/providers/openai) so their third-party dependencies
// are not pulled into the core SDK. A provider package registers itself in an init function via
// Register or RegisterFallback, following the database/sql driver pattern; applications activate
// a provider with a blank import:
//
//	import _ "github.com/launchdarkly/go-server-sdk-ai/providers/openai"
//
// CreateModel and CreateAgent resolve a registered provider for an AI Config (by the config's
// provider name, falling back to multi-provider packages) and delegate runner creation to it.
package providers

import (
	"context"
	"errors"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
)

// ErrNotSupported is returned by AIProvider factory methods for modes the provider does not
// support. Note that provider resolution treats any creation failure — not just ErrNotSupported —
// as "try the next candidate", matching the other LaunchDarkly AI SDKs; this sentinel exists so
// unsupported modes are distinguishable from real initialization failures in logs and errors.
var ErrNotSupported = errors.New("providers: mode not supported by this provider")

// Config defines the subset of an AI Config that a provider requires to construct a Runner. This
// is satisfied by *ldai.Config. (Defined as an interface here, rather than importing the ldai
// package, so the ldai package can depend on this package without a cycle.)
type Config interface {
	Messages() []datamodel.Message
	ModelName() string
	ProviderName() string
	ModelParam(key string) (ldvalue.Value, bool)
	ModelParams() map[string]ldvalue.Value
	CustomModelParam(key string) (ldvalue.Value, bool)
	CustomModelParams() map[string]ldvalue.Value
}

// Tool is a callable made available to an agent. The provider invokes it when the model requests
// the corresponding tool call.
type Tool func(ctx context.Context, args interface{}) (interface{}, error)

// ToolRegistry is a registry of callable tools keyed by tool name.
type ToolRegistry map[string]Tool

// AIProvider is a per-provider factory: it constructs focused Runner instances for AI Configs.
//
// Provider packages implement AIProvider by embedding UnimplementedAIProvider — which is required
// (it satisfies an unexported method) so that adding factory methods for new modes is not a
// breaking change — and overriding the modes they support. Factory methods must return a non-nil
// Runner or an error, never both nil. Providers register an instance via Register or
// RegisterFallback in an init function.
type AIProvider interface {
	// CreateModel creates a Runner for a completion or judge AI Config. multiTurn indicates
	// whether the runner should accumulate conversation history across successive Run calls
	// (chat semantics); judges pass false so each evaluation starts from the initial config
	// messages. Returns ErrNotSupported if the provider does not support model creation.
	CreateModel(ctx context.Context, config Config, multiTurn bool) (Runner, error)

	// CreateAgent creates a Runner for an agent AI Config with the given tools. Returns
	// ErrNotSupported if the provider does not support agent creation.
	CreateAgent(ctx context.Context, config Config, tools ToolRegistry) (Runner, error)

	// mustEmbedUnimplementedAIProvider forces implementations to embed UnimplementedAIProvider,
	// keeping this interface extensible without breaking providers.
	mustEmbedUnimplementedAIProvider()
}

// UnimplementedAIProvider returns ErrNotSupported from every AIProvider factory method. Provider
// implementations must embed it; they then only need to implement the modes they support.
type UnimplementedAIProvider struct{}

// CreateModel returns ErrNotSupported.
func (UnimplementedAIProvider) CreateModel(context.Context, Config, bool) (Runner, error) {
	return nil, ErrNotSupported
}

// CreateAgent returns ErrNotSupported.
func (UnimplementedAIProvider) CreateAgent(context.Context, Config, ToolRegistry) (Runner, error) {
	return nil, ErrNotSupported
}

func (UnimplementedAIProvider) mustEmbedUnimplementedAIProvider() {}
