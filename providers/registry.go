package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/launchdarkly/go-sdk-common/v4/ldlog"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

//nolint:gochecknoglobals // a package-level registry is what enables database/sql-style registration
var (
	registryMu sync.RWMutex
	registry   = map[string]AIProvider{}
	// fallbackOrder records fallback providers in registration order, for deterministic resolution.
	fallbackOrder []string
)

// normalizeName canonicalizes a provider name for registration and lookup; provider names are
// case-insensitive.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// Register makes a provider available for resolution under the given name (case-insensitive),
// which must match the provider name served in AI Configs (e.g. "openai"). It is intended to be
// called from a provider package's init function and panics if name or provider is invalid or if
// the name is already registered.
func Register(name string, provider AIProvider) {
	register(name, provider, false)
}

// RegisterFallback is Register for multi-provider packages (e.g. a LangChain-based provider) that
// can serve configs regardless of their provider name. Fallback providers are tried, in
// registration order (i.e. the order their packages were imported), when no provider is
// registered under a config's provider name.
func RegisterFallback(name string, provider AIProvider) {
	register(name, provider, true)
}

func register(name string, provider AIProvider, fallback bool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	name = normalizeName(name)
	if name == "" {
		panic("providers: Register name must not be empty")
	}
	if provider == nil {
		panic("providers: Register provider must not be nil")
	}
	if _, dup := registry[name]; dup {
		panic("providers: Register called twice for provider " + name)
	}
	registry[name] = provider
	if fallback {
		fallbackOrder = append(fallbackOrder, name)
	}
}

// CreateOption configures provider resolution. See WithProvider, WithDefaultProvider,
// WithMultiTurn, and WithLoggers.
type CreateOption func(*createOptions)

type createOptions struct {
	provider        AIProvider
	defaultProvider string
	multiTurn       bool
	multiTurnSet    bool
	loggers         interfaces.LDLoggers
}

// WithProvider supplies the AIProvider instance to use directly, bypassing the registry entirely.
// It takes precedence over WithDefaultProvider. This is useful for tests and for callers that
// construct their own provider rather than relying on init-time registration.
func WithProvider(provider AIProvider) CreateOption {
	return func(o *createOptions) { o.provider = provider }
}

// WithDefaultProvider restricts resolution to the named registered provider, skipping resolution
// by the config's provider name and the fallback providers. An empty (or whitespace-only) name
// leaves resolution unrestricted, matching the optional default-provider parameter in the other
// LaunchDarkly AI SDKs.
func WithDefaultProvider(name string) CreateOption {
	return func(o *createOptions) { o.defaultProvider = normalizeName(name) }
}

// WithMultiTurn sets whether the created runner should accumulate conversation history across
// successive Run calls. The default is true (chat semantics); judges use false so each evaluation
// starts from the initial config messages. It is only meaningful for CreateModel; CreateAgent
// logs a warning if it is supplied.
func WithMultiTurn(multiTurn bool) CreateOption {
	return func(o *createOptions) {
		o.multiTurn = multiTurn
		o.multiTurnSet = true
	}
}

// WithLoggers sets the loggers used to report resolution progress and failures.
func WithLoggers(loggers interfaces.LDLoggers) CreateOption {
	return func(o *createOptions) { o.loggers = loggers }
}

// CreateModel resolves a provider for the given completion or judge AI Config and creates a
// Runner with it. Resolution tries, in order: the WithProvider instance if set; the
// WithDefaultProvider override if set; the provider registered under the config's provider name;
// then each fallback provider in registration order. A provider that fails to create a runner
// (including with ErrNotSupported) causes resolution to move on to the next candidate.
func CreateModel(ctx context.Context, config Config, opts ...CreateOption) (Runner, error) {
	if config == nil {
		return nil, fmt.Errorf("providers: config must not be nil")
	}
	o := applyOptions(opts)
	return resolve(config, o, func(p AIProvider) (Runner, error) {
		return p.CreateModel(ctx, config, o.multiTurn)
	})
}

// CreateAgent resolves a provider for the given agent AI Config and creates a Runner with it,
// passing tools through to the provider. Resolution order is the same as CreateModel.
func CreateAgent(ctx context.Context, config Config, tools ToolRegistry, opts ...CreateOption) (Runner, error) {
	if config == nil {
		return nil, fmt.Errorf("providers: config must not be nil")
	}
	o := applyOptions(opts)
	if o.multiTurnSet {
		o.loggers.Warn("providers: WithMultiTurn has no effect on CreateAgent and was ignored")
	}
	return resolve(config, o, func(p AIProvider) (Runner, error) {
		return p.CreateAgent(ctx, config, tools)
	})
}

func applyOptions(opts []CreateOption) *createOptions {
	o := &createOptions{multiTurn: true, loggers: ldlog.NewDisabledLoggers()}
	for _, opt := range opts {
		opt(o)
	}
	if o.loggers == nil {
		o.loggers = ldlog.NewDisabledLoggers()
	}
	return o
}

// candidate pairs a provider with the name it resolves under, for logging and error reporting.
type candidate struct {
	name     string
	provider AIProvider
}

// candidatesFor builds the ordered list of providers to attempt, snapshotted under a single
// registry lock so resolution is unaffected by concurrent registration.
func candidatesFor(o *createOptions, configProviderName string) ([]candidate, error) {
	if o.provider != nil {
		return []candidate{{name: "<custom>", provider: o.provider}}, nil
	}

	registryMu.RLock()
	defer registryMu.RUnlock()

	if o.defaultProvider != "" {
		provider, ok := registry[o.defaultProvider]
		if !ok {
			return nil, fmt.Errorf(
				"providers: default provider %q is not registered (missing blank import of its provider package?)",
				o.defaultProvider)
		}
		return []candidate{{name: o.defaultProvider, provider: provider}}, nil
	}

	candidates := make([]candidate, 0, len(fallbackOrder)+1)
	specific := normalizeName(configProviderName)
	if provider, ok := registry[specific]; ok && specific != "" {
		candidates = append(candidates, candidate{name: specific, provider: provider})
	}
	for _, name := range fallbackOrder {
		if name == specific {
			continue
		}
		candidates = append(candidates, candidate{name: name, provider: registry[name]})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf(
			"providers: no provider is registered for %q and no fallback providers are registered",
			configProviderName)
	}
	return candidates, nil
}

// resolve tries each candidate provider in order, returning the first successfully created
// Runner. Failures are logged and resolution continues; the last failure is wrapped in the
// returned error if no candidate succeeds.
func resolve(config Config, o *createOptions, create func(AIProvider) (Runner, error)) (Runner, error) {
	candidates, err := candidatesFor(o, config.ProviderName())
	if err != nil {
		return nil, err
	}

	tried := make([]string, 0, len(candidates))
	var lastErr error
	for _, c := range candidates {
		tried = append(tried, c.name)
		o.loggers.Debugf("providers: attempting to create runner with provider %q", c.name)
		runner, err := create(c.provider)
		if err != nil {
			o.loggers.Warnf("providers: provider %q could not create runner: %v", c.name, err)
			lastErr = err
			continue
		}
		if runner == nil {
			o.loggers.Warnf("providers: provider %q returned no runner and no error", c.name)
			continue
		}
		o.loggers.Debugf("providers: created runner with provider %q", c.name)
		return runner, nil
	}

	err = fmt.Errorf("providers: no provider could create a runner (tried: %s)", strings.Join(tried, ", "))
	if lastErr != nil {
		err = fmt.Errorf("%w (last error: %w)", err, lastErr)
	}
	return nil, err
}
