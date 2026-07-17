package ldai

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"

	"github.com/alexkappa/mustache"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// Defines the Mustache variable name used to access the provided context.
const ldContextVariable = "ldctx"

// JudgePlaceholderMessageHistory and JudgePlaceholderResponseToEvaluate are the literal placeholder
// strings injected during judge config evaluation (pass 1) and consumed by Judge.buildMessages (pass 2).
// Both passes must use the same values or substitution silently fails.
const (
	JudgePlaceholderMessageHistory     = "{{message_history}}"
	JudgePlaceholderResponseToEvaluate = "{{response_to_evaluate}}"
)

// ServerSDK defines the required methods for the AI SDK to interact with LaunchDarkly. These methods are
// satisfied by the LaunchDarkly Go Server SDK.
type ServerSDK interface {
	JSONVariation(
		key string,
		context ldcontext.Context,
		defaultVal ldvalue.Value,
	) (ldvalue.Value, error)
	Loggers() interfaces.LDLoggers
	TrackMetric(
		eventName string,
		context ldcontext.Context,
		metricValue float64,
		data ldvalue.Value,
	) error
}

// Client is the main entrypoint for the AI SDK. A client can be used to obtain an AI Config from LaunchDarkly.
// Unless otherwise noted, the Client's method are not safe for concurrent use.
type Client struct {
	sdk    ServerSDK
	logger interfaces.LDLoggers
}

const (
	sdkInfoEvent          = "$ld:ai:sdk:info"
	usageCompletionConfig = "$ld:ai:usage:completion-config"
)

// NewClient creates a new AI Client. The provided SDK interface must not be nil. The client will use the provided SDK's
// loggers to log warnings and errors.
func NewClient(sdk ServerSDK) (*Client, error) {
	if sdk == nil {
		return nil, fmt.Errorf("sdk must not be nil")
	}
	c := &Client{
		sdk:    sdk,
		logger: sdk.Loggers(),
	}
	if err := c.trackSDKInfo(); err != nil {
		c.logger.Warnf("AI Client: failed to track SDK info: %v", err)
	}
	return c, nil
}

func (c *Client) trackSDKInfo() error {
	ctx, err := ldcontext.NewBuilder("ld-internal-tracking").Kind("ld_ai").Anonymous(true).TryBuild()
	if err != nil {
		return err
	}
	data := ldvalue.ObjectBuild().
		Set("aiSdkName", ldvalue.String(SDKName)).
		Set("aiSdkVersion", ldvalue.String(Version)).
		Set("aiSdkLanguage", ldvalue.String(SDKLanguage)).
		Build()
	return c.sdk.TrackMetric(sdkInfoEvent, ctx, 1, data)
}

func (c *Client) logConfigWarning(key string, format string, args ...interface{}) {
	prefix := "AI Config '" + key + "': "
	c.logger.Warnf(prefix+format, args...)
}

// CompletionConfig retrieves an AI Config and interpolates its message templates using the provided
// variables. Returns the default value if the config cannot be evaluated. Template interpolation is
// not applied to the default value's messages.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned Config to obtain a Tracker.
func (c *Client) CompletionConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageCompletionConfig, context, 1, data)
	return c.evaluateConfig(key, context, defaultValue, variables)
}

// CreateTracker reconstructs a Tracker from a resumption token and the given context.
// This delegates to TrackerFromResumptionToken. See that function for details.
func (c *Client) CreateTracker(token string, context ldcontext.Context) (*Tracker, error) {
	return TrackerFromResumptionToken(token, c.sdk, context)
}

// returnDefault sets a tracker factory on a copy of def (so CreateTracker always works) and
// returns the resulting Config. Used for all error-path returns in evaluateConfig.
func (c *Client) returnDefault(key string, context ldcontext.Context, def Config) Config {
	def.key = key
	def.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, def.VariationKey(), def.Version(), context, &def, c.logger, "")
	}
	return def
}

// evaluateShared fetches, validates, unmarshals, and interpolates a config. On any failure it
// returns ok=false; the caller is responsible for returning its own typed default. On success it
// returns the parsed wire config, the interpolated messages, and the resolved tools.
func (c *Client) evaluateShared(
	key string,
	context ldcontext.Context,
	defaultLdValue ldvalue.Value,
	variables map[string]interface{},
) (
	parsed datamodel.Config,
	interpolated []datamodel.Message,
	tools map[string]ToolConfig,
	ok bool,
) {
	result, err := c.sdk.JSONVariation(key, context, defaultLdValue)
	if err != nil {
		return datamodel.Config{}, nil, nil, false
	}

	if result.Type() != ldvalue.ObjectType {
		c.logConfigWarning(key, "unmarshalling failed, expected JSON object but got %s", result.Type().String())
		return datamodel.Config{}, nil, nil, false
	}

	if err := json.Unmarshal(result.AsRaw(), &parsed); err != nil {
		c.logConfigWarning(key, "unmarshalling failed: %v", err)
		return datamodel.Config{}, nil, nil, false
	}

	mergedVariables := map[string]interface{}{
		ldContextVariable: getAllAttributes(context),
	}
	for k, v := range variables {
		if k == ldContextVariable {
			c.logConfigWarning(key, "config variables contains 'ldctx', which is reserved and cannot be overwritten")
			continue
		}
		mergedVariables[k] = v
	}

	msgs := make([]datamodel.Message, 0, len(parsed.Messages))
	for i, msg := range parsed.Messages {
		content, err := interpolateTemplate(msg.Content, mergedVariables)
		if err != nil {
			c.logConfigWarning(key, "malformed message at index %d: %v", i, err)
			return datamodel.Config{}, nil, nil, false
		}
		msgs = append(msgs, datamodel.Message{Content: content, Role: msg.Role})
	}

	return parsed, msgs, c.resolveTools(key, result), true
}

// evaluateConfig fetches and interpolates an AI Config without emitting any metric.
// Callers are meant to emit their own metric before calling this.
func (c *Client) evaluateConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	parsed, interpolatedMessages, tools, ok := c.evaluateShared(key, context, defaultValue.AsLdValue(), variables)
	if !ok {
		return c.returnDefault(key, context, defaultValue)
	}

	// Build raw with interpolated messages for AsLdValue(). Keep all other fields from
	// the wire response so that model.parameters.tools[] is preserved verbatim.
	raw := parsed
	raw.Messages = interpolatedMessages

	cfg := AICompletionConfig{
		aiConfigBase: aiConfigBase{
			key:          key,
			enabled:      parsed.Meta.Enabled,
			variationKey: parsed.Meta.VariationKey,
			version:      defaultVersion(parsed.Meta.Version),
			model: ModelConfig{
				Name:       parsed.Model.Name,
				Parameters: maps.Clone(parsed.Model.Parameters),
				Custom:     maps.Clone(parsed.Model.Custom),
			},
			provider: ProviderConfig{Name: parsed.Provider.Name},
			tools:    tools,
		},
		messages:             interpolatedMessages,
		judgeConfiguration:   parsed.JudgeConfiguration.Clone(),
		evaluationMetricKey:  parsed.EvaluationMetricKey,
		evaluationMetricKeys: slices.Clone(parsed.EvaluationMetricKeys),
		raw:                  raw,
	}

	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.VariationKey(), cfg.Version(), context, &cfg, c.logger, "")
	}

	return cfg
}

func getAllAttributes(context ldcontext.Context) map[string]interface{} {
	if !context.Multiple() {
		return addContextAttributes(context, false)
	}

	attributes := map[string]interface{}{
		"kind": context.Kind(),
		"key":  context.FullyQualifiedKey(),
	}

	for _, ctx := range context.GetAllIndividualContexts(nil) {
		attributes[string(ctx.Kind())] = addContextAttributes(ctx, true)
	}

	return attributes
}

func addContextAttributes(context ldcontext.Context, omitKind bool) map[string]interface{} {
	attributes := map[string]interface{}{
		"key":       context.Key(),
		"anonymous": context.Anonymous(),
	}

	if !omitKind {
		attributes["kind"] = context.Kind()
	}

	for _, attr := range context.GetOptionalAttributeNames(nil) {
		attributes[attr] = context.GetValue(attr).AsArbitraryValue()
	}

	return attributes
}

// Matches {{{triple}}} tags first so they are never mistaken for double-mustache tags.
var mustacheTagPattern = regexp.MustCompile(`\{\{\{[^}]*\}\}\}|\{\{[^}]*\}\}`)

// disableHTMLEscaping rewrites plain {{var}} tags as unescaped {{&var}} tags. Interpolated
// messages are sent to AI models, not rendered as HTML, but the mustache library HTML-escapes
// {{var}} values and offers no global way to turn that off. Sigil tags ({{{...}}}, {{#...}},
// {{/...}}, {{^...}}, {{!...}}, {{>...}}, {{=...}}, {{&...}}) are left untouched.
func disableHTMLEscaping(template string) string {
	return mustacheTagPattern.ReplaceAllStringFunc(template, func(tag string) string {
		if len(tag) > 4 {
			switch tag[2] {
			case '{', '#', '/', '^', '!', '>', '=', '&':
				return tag
			}
		}
		return "{{&" + tag[2:]
	})
}

func interpolateTemplate(template string, variables map[string]interface{}) (string, error) {
	m := mustache.New()
	if err := m.ParseString(disableHTMLEscaping(template)); err != nil {
		return "", err
	}
	return m.RenderString(variables)
}

// resolveTools determines the tools map to expose on the config.
// The root-level "tools" key is authoritative (even when empty), suppressing any legacy fallback.
// When the root key is absent, the legacy model.parameters.tools[] array is used instead.
// Malformed or unnamed entries in the legacy array are skipped with a warning; the config is
// still returned (not defaulted). Root-level entries are always valid objects because
// json.Unmarshal would have failed before this function is reached if they were not.
func (c *Client) resolveTools(key string, served ldvalue.Value) map[string]ToolConfig {
	// Check for the presence of the root "tools" key (absent ≠ null).
	toolsKeyPresent := slices.Contains(served.Keys(nil), "tools")

	if toolsKeyPresent {
		toolsVal := served.GetByKey("tools")
		if toolsVal.Type() != ldvalue.ObjectType {
			// Key present but value is not an object (e.g. null). No tools, no fallback.
			return nil
		}
		tools := make(map[string]ToolConfig, toolsVal.Count())
		for _, k := range toolsVal.Keys(nil) {
			tools[k] = toolConfigFromRawValue(toolsVal.GetByKey(k), k)
		}
		return emptyToNil(tools)
	}

	// Root key absent — fall back to model.parameters.tools[].
	legacyTools := served.GetByKey("model").GetByKey("parameters").GetByKey("tools")
	if legacyTools.Type() != ldvalue.ArrayType {
		return nil
	}

	tools := make(map[string]ToolConfig)
	for i := range legacyTools.Count() {
		v := legacyTools.GetByIndex(i)
		if v.Type() != ldvalue.ObjectType {
			c.logConfigWarning(key, "model.parameters.tools[%d] is not an object; skipping", i)
			continue
		}
		name := v.GetByKey("name").StringValue()
		if name == "" {
			c.logConfigWarning(key, "model.parameters.tools[%d] has no 'name'; skipping", i)
			continue
		}
		tools[name] = toolConfigFromRawValue(v, name)
	}
	return emptyToNil(tools)
}

// emptyToNil returns nil when tools is empty so that "no tools" is uniform across all callers.
func emptyToNil(tools map[string]ToolConfig) map[string]ToolConfig {
	if len(tools) == 0 {
		return nil
	}
	return tools
}

// defaultVersion returns the dereferenced version, or 1 when absent from the wire.
func defaultVersion(v *int) int {
	if v == nil {
		return 1
	}
	return *v
}
