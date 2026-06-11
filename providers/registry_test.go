package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v4/ldlog"
	"github.com/launchdarkly/go-sdk-common/v4/ldlogtest"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetRegistry restores a clean registry for each test. The registry is a package-level global
// (by design, for init-time registration), so tests must not run in parallel.
func resetRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]AIProvider{}
	fallbackOrder = nil
}

type testConfig struct {
	providerName string
	modelParams  map[string]ldvalue.Value
	customParams map[string]ldvalue.Value
}

func (c *testConfig) Messages() []datamodel.Message { return nil }
func (c *testConfig) ModelName() string             { return "test-model" }
func (c *testConfig) ProviderName() string          { return c.providerName }
func (c *testConfig) ModelParam(key string) (ldvalue.Value, bool) {
	val, ok := c.modelParams[key]
	return val, ok
}
func (c *testConfig) ModelParams() map[string]ldvalue.Value { return c.modelParams }
func (c *testConfig) CustomModelParam(key string) (ldvalue.Value, bool) {
	val, ok := c.customParams[key]
	return val, ok
}
func (c *testConfig) CustomModelParams() map[string]ldvalue.Value { return c.customParams }

type testRunner struct {
	name string
}

func (r *testRunner) Run(context.Context, string, map[string]interface{}) (RunnerResult, error) {
	return RunnerResult{Content: r.name}, nil
}

// testProvider records factory calls and returns a configured runner or error.
type testProvider struct {
	UnimplementedAIProvider
	name       string
	createErr  error
	returnNil  bool
	modelCalls int
	agentCalls int
	multiTurns []bool
	gotTools   ToolRegistry
}

func (p *testProvider) CreateModel(_ context.Context, _ Config, multiTurn bool) (Runner, error) {
	p.modelCalls++
	p.multiTurns = append(p.multiTurns, multiTurn)
	if p.createErr != nil {
		return nil, p.createErr
	}
	if p.returnNil {
		return nil, nil
	}
	return &testRunner{name: p.name}, nil
}

func (p *testProvider) CreateAgent(_ context.Context, _ Config, tools ToolRegistry) (Runner, error) {
	p.agentCalls++
	p.gotTools = tools
	if p.createErr != nil {
		return nil, p.createErr
	}
	return &testRunner{name: p.name}, nil
}

func runnerName(t *testing.T, r Runner) string {
	t.Helper()
	result, err := r.Run(context.Background(), "input", nil)
	require.NoError(t, err)
	return result.Content
}

func TestRegister_Validation(t *testing.T) {
	resetRegistry(t)

	assert.PanicsWithValue(t, "providers: Register name must not be empty", func() {
		Register("  ", &testProvider{})
	})
	assert.PanicsWithValue(t, "providers: Register provider must not be nil", func() {
		Register("openai", nil)
	})

	Register("openai", &testProvider{})
	assert.PanicsWithValue(t, "providers: Register called twice for provider openai", func() {
		Register("OpenAI", &testProvider{}) // names are case-insensitive
	})
}

func TestCreateModel_ResolvesByConfigProviderName(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	Register("OpenAI", openai)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.NoError(t, err)
	assert.Equal(t, "openai", runnerName(t, runner))
	assert.Equal(t, 1, openai.modelCalls)
}

func TestCreateModel_ProviderNameIsCaseInsensitive(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	Register("openai", openai)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "OpenAI"})
	require.NoError(t, err)
	assert.Equal(t, "openai", runnerName(t, runner))
}

func TestCreateModel_FallsBackToFallbackProviders(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	langchain := &testProvider{name: "langchain"}
	Register("openai", openai)
	RegisterFallback("langchain", langchain)

	// Unknown provider name: only the fallback should be tried.
	runner, err := CreateModel(context.Background(), &testConfig{providerName: "anthropic"})
	require.NoError(t, err)
	assert.Equal(t, "langchain", runnerName(t, runner))
	assert.Equal(t, 0, openai.modelCalls)
	assert.Equal(t, 1, langchain.modelCalls)
}

func TestCreateModel_TriesSpecificProviderBeforeFallbacks(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	langchain := &testProvider{name: "langchain"}
	Register("openai", openai)
	RegisterFallback("langchain", langchain)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.NoError(t, err)
	assert.Equal(t, "openai", runnerName(t, runner))
	assert.Equal(t, 0, langchain.modelCalls)
}

func TestCreateModel_SkipsFailingProvider(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai", createErr: ErrNotSupported}
	langchain := &testProvider{name: "langchain"}
	Register("openai", openai)
	RegisterFallback("langchain", langchain)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.NoError(t, err)
	assert.Equal(t, "langchain", runnerName(t, runner))
	assert.Equal(t, 1, openai.modelCalls)
}

func TestCreateModel_FallbackOrderIsRegistrationOrder(t *testing.T) {
	resetRegistry(t)
	first := &testProvider{name: "first", createErr: errors.New("boom")}
	second := &testProvider{name: "second"}
	RegisterFallback("first", first)
	RegisterFallback("second", second)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "unknown"})
	require.NoError(t, err)
	assert.Equal(t, "second", runnerName(t, runner))
	assert.Equal(t, 1, first.modelCalls)
}

func TestCreateModel_DefaultProviderOverrideSkipsResolution(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	custom := &testProvider{name: "custom"}
	Register("openai", openai)
	Register("custom", custom)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"},
		WithDefaultProvider("Custom"))
	require.NoError(t, err)
	assert.Equal(t, "custom", runnerName(t, runner))
	assert.Equal(t, 0, openai.modelCalls)
}

func TestCreateModel_DefaultProviderOverrideDoesNotFallBack(t *testing.T) {
	resetRegistry(t)
	langchain := &testProvider{name: "langchain"}
	RegisterFallback("langchain", langchain)

	_, err := CreateModel(context.Background(), &testConfig{providerName: "openai"},
		WithDefaultProvider("missing"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"missing"`,
		"error must name the default provider that was actually tried")
	assert.Equal(t, 0, langchain.modelCalls)
}

func TestCreateModel_EmptyDefaultProviderLeavesResolutionUnrestricted(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	Register("openai", openai)

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"},
		WithDefaultProvider("  "))
	require.NoError(t, err)
	assert.Equal(t, "openai", runnerName(t, runner))
}

func TestCreateModel_NoProvidersRegistered(t *testing.T) {
	resetRegistry(t)

	_, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider is registered")
}

func TestCreateModel_NilConfig(t *testing.T) {
	resetRegistry(t)
	Register("openai", &testProvider{name: "openai"})

	_, err := CreateModel(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config must not be nil")

	_, err = CreateAgent(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config must not be nil")
}

func TestCreateModel_WithProviderBypassesRegistry(t *testing.T) {
	resetRegistry(t) // nothing registered at all
	custom := &testProvider{name: "custom"}

	runner, err := CreateModel(context.Background(), &testConfig{providerName: "openai"},
		WithProvider(custom))
	require.NoError(t, err)
	assert.Equal(t, "custom", runnerName(t, runner))
	assert.Equal(t, 1, custom.modelCalls)
}

func TestCreateModel_NilRunnerNilErrorSkipsProvider(t *testing.T) {
	resetRegistry(t)
	broken := &testProvider{name: "broken", returnNil: true}
	langchain := &testProvider{name: "langchain"}
	Register("broken", broken)
	RegisterFallback("langchain", langchain)

	mockLog := ldlogtest.NewMockLog()
	runner, err := CreateModel(context.Background(), &testConfig{providerName: "broken"},
		WithLoggers(mockLog.Loggers))
	require.NoError(t, err)
	assert.Equal(t, "langchain", runnerName(t, runner))
	mockLog.AssertMessageMatch(t, true, ldlog.Warn, `provider "broken" returned no runner and no error`)
}

func TestCreateModel_ErrorNamesTriedProviders(t *testing.T) {
	resetRegistry(t)
	Register("openai", &testProvider{name: "openai", createErr: ErrNotSupported})
	RegisterFallback("langchain", &testProvider{name: "langchain", createErr: ErrNotSupported})

	_, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tried: openai, langchain")
}

func TestCreateAgent_WithMultiTurnWarns(t *testing.T) {
	resetRegistry(t)
	Register("openai", &testProvider{name: "openai"})

	mockLog := ldlogtest.NewMockLog()
	_, err := CreateAgent(context.Background(), &testConfig{providerName: "openai"}, nil,
		WithMultiTurn(false), WithLoggers(mockLog.Loggers))
	require.NoError(t, err)
	mockLog.AssertMessageMatch(t, true, ldlog.Warn, "WithMultiTurn has no effect on CreateAgent")
}

func TestCreateModel_AllProvidersFailIncludesLastError(t *testing.T) {
	resetRegistry(t)
	boom := errors.New("boom")
	RegisterFallback("langchain", &testProvider{name: "langchain", createErr: boom})

	_, err := CreateModel(context.Background(), &testConfig{providerName: "unknown"})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestCreateModel_MultiTurn(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	Register("openai", openai)

	cfg := &testConfig{providerName: "openai"}

	_, err := CreateModel(context.Background(), cfg)
	require.NoError(t, err)
	_, err = CreateModel(context.Background(), cfg, WithMultiTurn(false))
	require.NoError(t, err)

	assert.Equal(t, []bool{true, false}, openai.multiTurns)
}

func TestCreateAgent_PassesTools(t *testing.T) {
	resetRegistry(t)
	openai := &testProvider{name: "openai"}
	Register("openai", openai)

	tools := ToolRegistry{
		"search": func(context.Context, interface{}) (interface{}, error) { return "ok", nil },
	}

	runner, err := CreateAgent(context.Background(), &testConfig{providerName: "openai"}, tools)
	require.NoError(t, err)
	assert.Equal(t, "openai", runnerName(t, runner))
	assert.Equal(t, 1, openai.agentCalls)
	require.Contains(t, openai.gotTools, "search")
}

func TestUnimplementedAIProvider_ReturnsErrNotSupported(t *testing.T) {
	var p UnimplementedAIProvider

	_, err := p.CreateModel(context.Background(), &testConfig{}, true)
	assert.ErrorIs(t, err, ErrNotSupported)

	_, err = p.CreateAgent(context.Background(), &testConfig{}, nil)
	assert.ErrorIs(t, err, ErrNotSupported)
}

// modelOnlyProvider embeds UnimplementedAIProvider and overrides only CreateModel, the intended
// implementation pattern for provider packages.
type modelOnlyProvider struct {
	UnimplementedAIProvider
}

func (p *modelOnlyProvider) CreateModel(context.Context, Config, bool) (Runner, error) {
	return &testRunner{name: "model-only"}, nil
}

func TestPartialProvider_UnsupportedModeFallsThrough(t *testing.T) {
	resetRegistry(t)
	langchain := &testProvider{name: "langchain"}
	Register("model-only", &modelOnlyProvider{})
	RegisterFallback("langchain", langchain)

	cfg := &testConfig{providerName: "model-only"}

	runner, err := CreateModel(context.Background(), cfg)
	require.NoError(t, err)
	assert.Equal(t, "model-only", runnerName(t, runner))

	// Agent creation is not supported by model-only, so resolution falls through to langchain.
	runner, err = CreateAgent(context.Background(), cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "langchain", runnerName(t, runner))
}

func TestResolutionLogging(t *testing.T) {
	resetRegistry(t)
	Register("openai", &testProvider{name: "openai", createErr: ErrNotSupported})
	RegisterFallback("langchain", &testProvider{name: "langchain"})

	mockLog := ldlogtest.NewMockLog()
	mockLog.Loggers.SetMinLevel(ldlog.Debug)
	_, err := CreateModel(context.Background(), &testConfig{providerName: "openai"},
		WithLoggers(mockLog.Loggers))
	require.NoError(t, err)

	mockLog.AssertMessageMatch(t, true, ldlog.Debug, `attempting to create runner with provider "openai"`)
	mockLog.AssertMessageMatch(t, true, ldlog.Warn, `provider "openai" could not create runner`)
	mockLog.AssertMessageMatch(t, true, ldlog.Debug, `created runner with provider "langchain"`)
}

func TestCreateModel_ProviderErrorMessage(t *testing.T) {
	resetRegistry(t)
	Register("openai", &testProvider{name: "openai", createErr: fmt.Errorf("missing API key")})

	_, err := CreateModel(context.Background(), &testConfig{providerName: "openai"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing API key")
}
