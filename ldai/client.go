package ldai

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

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
	usageJudgeConfig      = "$ld:ai:usage:judge-config"
	usageAgentConfig      = "$ld:ai:usage:agent-config"
	usageAgentConfigs     = "$ld:ai:usage:agent-configs"
	usageAgentGraph       = "$ld:ai:usage:agent-graph"

	usageCompletionConfigTemplate = "$ld:ai:usage:completion-config-template"
	usageAgentConfigTemplate      = "$ld:ai:usage:agent-config-template"
	usageJudgeConfigTemplate      = "$ld:ai:usage:judge-config-template"
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

// CompletionConfig retrieves an AI completion config and interpolates its message templates using
// the provided variables. Returns the default value if the config cannot be evaluated. Template
// interpolation is not applied to the default value's messages.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AICompletionConfig to obtain a Tracker.
func (c *Client) CompletionConfig(
	key string,
	context ldcontext.Context,
	defaultValue AICompletionConfigDefault,
	variables map[string]interface{},
) AICompletionConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageCompletionConfig, context, 1, data)
	return c.evaluateConfig(key, context, defaultValue, variables, true)
}

// CompletionConfigTemplate retrieves an AI completion config without performing Mustache
// interpolation on message content. Useful when the caller wants to perform its own interpolation
// or pass raw templates to an external system. Returns the default value if the config cannot be
// evaluated.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AICompletionConfig to obtain a Tracker.
func (c *Client) CompletionConfigTemplate(
	key string,
	context ldcontext.Context,
	defaultValue AICompletionConfigDefault,
) AICompletionConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageCompletionConfigTemplate, context, 1, data)
	return c.evaluateConfig(key, context, defaultValue, nil, false)
}

// AgentConfigTemplate retrieves an AI agent config without performing Mustache interpolation on
// its instructions. Useful when the caller wants to perform its own interpolation or pass the raw
// template to an external system. Returns the default value if the config cannot be evaluated.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AIAgentConfig to obtain a Tracker.
func (c *Client) AgentConfigTemplate(
	key string,
	context ldcontext.Context,
	defaultValue AIAgentConfigDefault,
) AIAgentConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageAgentConfigTemplate, context, 1, data)
	return c.evaluateAgentConfig(key, context, defaultValue, nil, "", false)
}

// JudgeConfigTemplate retrieves an AI judge config without performing Mustache interpolation on
// its messages. Useful when the caller wants to perform its own interpolation or pass raw templates
// to an external system. Returns the default value if the config cannot be evaluated.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AIJudgeConfig to obtain a Tracker.
func (c *Client) JudgeConfigTemplate(
	key string,
	context ldcontext.Context,
	defaultValue AIJudgeConfigDefault,
) AIJudgeConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageJudgeConfigTemplate, context, 1, data)
	return c.evaluateJudgeConfig(key, context, defaultValue, nil, false)
}

// CreateTracker reconstructs a Tracker from a resumption token and the given context.
// This delegates to TrackerFromResumptionToken. See that function for details.
func (c *Client) CreateTracker(token string, context ldcontext.Context) (*Tracker, error) {
	return TrackerFromResumptionToken(token, c.sdk, context)
}

// returnDefault builds an AICompletionConfig from the provided default, wires a tracker factory,
// and returns it. Used for all error-path returns in evaluateConfig.
func (c *Client) returnDefault(
	key string,
	context ldcontext.Context,
	def AICompletionConfigDefault,
) AICompletionConfig {
	cfg := AICompletionConfig{
		aiConfigBase: aiConfigBase{
			key:     key,
			enabled: def.enabled,
			version: defaultVersion(nil),
			model: ModelConfig{
				Name:       def.modelName,
				Parameters: maps.Clone(def.modelParams),
				Custom:     maps.Clone(def.modelCustom),
			},
			provider: ProviderConfig{Name: def.providerName},
			tools:    c.resolveTools(key, def.AsLdValue()),
		},
		mode:               "",
		messages:           slices.Clone(def.messages),
		judgeConfiguration: def.judgeConfiguration.Clone(),
	}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.VariationKey(), cfg.Version(), context, &cfg, c.logger, "")
	}
	return cfg
}

// evaluateShared fetches, validates, and unmarshals a config, optionally interpolating messages.
// On any failure it returns ok=false; the caller is responsible for returning its typed default.
func (c *Client) evaluateShared(
	key string,
	context ldcontext.Context,
	defaultLdValue ldvalue.Value,
	variables map[string]interface{},
	interpolateMessages bool,
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

	if !interpolateMessages {
		return parsed, parsed.Messages, c.resolveTools(key, result), true
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

// evaluateConfig fetches and optionally interpolates a completion config without emitting any
// metric. CompletionConfig emits its own metric before calling this.
func (c *Client) evaluateConfig(
	key string,
	context ldcontext.Context,
	defaultValue AICompletionConfigDefault,
	variables map[string]interface{},
	interpolate bool,
) AICompletionConfig {
	parsed, interpolatedMessages, tools, ok := c.evaluateShared(
		key, context, defaultValue.AsLdValue(), variables, interpolate)
	if !ok {
		return c.returnDefault(key, context, defaultValue)
	}

	// A present, non-"completion" mode is a mismatch; a missing mode defaults to "completion".
	if parsed.Meta.Mode != "" && parsed.Meta.Mode != "completion" {
		c.logConfigWarning(key, "expected mode %q but got %q; using default", "completion", parsed.Meta.Mode)
		return c.returnDefault(key, context, defaultValue)
	}

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
		mode:                 parsed.Meta.Mode,
		messages:             interpolatedMessages,
		judgeConfiguration:   parsed.JudgeConfiguration.Clone(),
		evaluationMetricKey:  parsed.EvaluationMetricKey,
		evaluationMetricKeys: slices.Clone(parsed.EvaluationMetricKeys),
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

// returnJudgeDefault builds an AIJudgeConfig from the provided default, wires a tracker factory,
// and returns it. Used for all error-path returns in evaluateJudgeConfig.
func (c *Client) returnJudgeDefault(key string, context ldcontext.Context, def AIJudgeConfigDefault) AIJudgeConfig {
	cfg := AIJudgeConfig{
		aiConfigBase: aiConfigBase{
			key:     key,
			enabled: def.enabled,
			version: defaultVersion(nil),
			model: ModelConfig{
				Name:       def.modelName,
				Parameters: maps.Clone(def.modelParams),
				Custom:     maps.Clone(def.modelCustom),
			},
			provider: ProviderConfig{Name: def.providerName},
			tools:    c.resolveTools(key, def.AsLdValue()),
		},
		messages:            slices.Clone(def.messages),
		evaluationMetricKey: def.evaluationMetricKey,
	}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.variationKey, cfg.version, context, &cfg, c.logger, "")
	}
	return cfg
}

// evaluateJudgeConfig builds a judge config without emitting a usage event. When interpolate is false,
// messages are returned verbatim and the reserved judge placeholders are not injected.
func (c *Client) evaluateJudgeConfig(
	key string,
	context ldcontext.Context,
	defaultValue AIJudgeConfigDefault,
	variables map[string]interface{},
	interpolate bool,
) AIJudgeConfig {
	var queryVariables map[string]interface{}
	if interpolate {
		// Extend variables with reserved judge placeholders so that {{message_history}} and
		// {{response_to_evaluate}} survive the first Mustache pass for substitution by
		// Judge.buildMessages during evaluation.
		extendedVariables := make(map[string]interface{})
		for k, v := range variables {
			if k == "message_history" || k == "response_to_evaluate" {
				c.logger.Warnf("AI Config '%s': variable '%s' is reserved by judge and will be ignored", key, k)
				continue
			}
			extendedVariables[k] = v
		}
		extendedVariables["message_history"] = JudgePlaceholderMessageHistory
		extendedVariables["response_to_evaluate"] = JudgePlaceholderResponseToEvaluate
		queryVariables = extendedVariables
	}

	parsed, interpolated, tools, ok := c.evaluateShared(
		key, context, defaultValue.AsLdValue(), queryVariables, interpolate)
	if !ok {
		return c.returnJudgeDefault(key, context, defaultValue)
	}

	// Mode-mismatch validation: a missing mode defaults to "completion" per spec, which mismatches "judge".
	if parsed.Meta.Mode != "judge" {
		c.logConfigWarning(key, "expected mode %q but got %q; using default", "judge", parsed.Meta.Mode)
		return c.returnJudgeDefault(key, context, defaultValue)
	}

	// Prefer the singular evaluationMetricKey; fall back to the first non-empty deprecated
	// evaluationMetricKeys entry for backward compatibility (matches JS/.NET behavior).
	metricKey := strings.TrimSpace(parsed.EvaluationMetricKey)
	if metricKey == "" {
		for _, k := range parsed.EvaluationMetricKeys {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				metricKey = trimmed
				break
			}
		}
	}

	cfg := AIJudgeConfig{
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
		messages:            interpolated,
		evaluationMetricKey: metricKey,
	}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.variationKey, cfg.version, context, &cfg, c.logger, "")
	}
	return cfg
}

// JudgeConfig retrieves a Judge AI Config and interpolates its message templates. The reserved
// variables message_history and response_to_evaluate are preserved as literal placeholders for
// substitution by Judge.buildMessages during evaluation.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AIJudgeConfig to obtain a Tracker.
func (c *Client) JudgeConfig(
	key string,
	context ldcontext.Context,
	defaultValue AIJudgeConfigDefault,
	variables map[string]interface{},
) AIJudgeConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageJudgeConfig, context, 1, data)
	return c.evaluateJudgeConfig(key, context, defaultValue, variables, true)
}

// returnAgentDefault builds an AIAgentConfig from the provided default, wires a tracker factory,
// and returns it. Used for all error-path returns in evaluateAgentConfig.
func (c *Client) returnAgentDefault(
	key string,
	context ldcontext.Context,
	def AIAgentConfigDefault,
	graphKey string,
) AIAgentConfig {
	cfg := AIAgentConfig{
		aiConfigBase: aiConfigBase{
			key:     key,
			enabled: def.enabled,
			version: defaultVersion(nil),
			model: ModelConfig{
				Name:       def.modelName,
				Parameters: maps.Clone(def.modelParams),
				Custom:     maps.Clone(def.modelCustom),
			},
			provider: ProviderConfig{Name: def.providerName},
			tools:    c.resolveTools(key, def.AsLdValue()),
		},
		instructions:       def.instructions,
		judgeConfiguration: def.judgeConfiguration.Clone(),
	}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.variationKey, cfg.version, context, &cfg, c.logger, graphKey)
	}
	return cfg
}

// evaluateAgentConfig builds an agent config without emitting a usage event. graphKey is empty for
// standalone calls. When interpolate is false, the instructions template is returned verbatim.
func (c *Client) evaluateAgentConfig(
	key string,
	context ldcontext.Context,
	defaultValue AIAgentConfigDefault,
	variables map[string]interface{},
	graphKey string,
	interpolate bool,
) AIAgentConfig {
	parsed, _, tools, ok := c.evaluateShared(key, context, defaultValue.AsLdValue(), variables, false)
	if !ok {
		return c.returnAgentDefault(key, context, defaultValue, graphKey)
	}

	// Mode-mismatch validation: a missing mode defaults to "completion" per spec, which mismatches "agent".
	if parsed.Meta.Mode != "agent" {
		c.logConfigWarning(key, "expected mode %q but got %q; using default", "agent", parsed.Meta.Mode)
		return c.returnAgentDefault(key, context, defaultValue, graphKey)
	}

	// When interpolate is true, run the instructions template through the Mustache engine using
	// the same merged variable map (ldctx + user vars) used for messages in evaluateShared.
	instructions := parsed.Instructions
	if interpolate && instructions != "" {
		mergedVars := map[string]interface{}{ldContextVariable: getAllAttributes(context)}
		for k, v := range variables {
			if k != ldContextVariable {
				mergedVars[k] = v
			}
		}
		if interpolated, err := interpolateTemplate(instructions, mergedVars); err == nil {
			instructions = interpolated
		} else {
			c.logConfigWarning(key, "malformed instructions template: %v", err)
			return c.returnAgentDefault(key, context, defaultValue, graphKey)
		}
	}

	cfg := AIAgentConfig{
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
		instructions:       instructions,
		judgeConfiguration: parsed.JudgeConfiguration.Clone(),
	}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.variationKey, cfg.version, context, &cfg, c.logger, graphKey)
	}
	return cfg
}

// AgentConfig retrieves an AI agent config and interpolates its instruction template using the
// provided variables. Returns the default value if the config cannot be evaluated.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned AIAgentConfig to obtain a Tracker.
func (c *Client) AgentConfig(
	key string,
	context ldcontext.Context,
	defaultValue AIAgentConfigDefault,
	variables map[string]interface{},
) AIAgentConfig {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageAgentConfig, context, 1, data)
	return c.evaluateAgentConfig(key, context, defaultValue, variables, "", true)
}

// AgentConfigRequest pairs a flag key with its per-agent default and interpolation variables for
// use with AgentConfigs. Each request is evaluated independently.
type AgentConfigRequest struct {
	Key          string
	DefaultValue AIAgentConfigDefault
	Variables    map[string]interface{}
}

// AgentConfigs retrieves multiple agent configs in batch. Each request is evaluated independently
// using its own default and variables; failed evaluations use the request's DefaultValue.
// Emits a single $ld:ai:usage:agent-configs event with the count of requested agents.
// Does NOT emit per-agent $ld:ai:usage:agent-config events.
func (c *Client) AgentConfigs(
	requests []AgentConfigRequest,
	context ldcontext.Context,
) map[string]AIAgentConfig {
	count := len(requests)
	_ = c.sdk.TrackMetric(usageAgentConfigs, context, float64(count), ldvalue.Int(count))
	result := make(map[string]AIAgentConfig, count)
	for _, req := range requests {
		result[req.Key] = c.evaluateAgentConfig(req.Key, context, req.DefaultValue, req.Variables, "", true)
	}
	return result
}

// AgentGraph retrieves and validates an agent graph for the given graphKey. Always returns a
// non-nil AgentGraphDefinition. On any validation failure the definition is disabled
// (Enabled() == false) with an empty node map; traversals are no-ops.
//
// Pass nil for variables when no interpolation variables are needed.
//
// Emits a single $ld:ai:usage:agent-graph event. Node agent configs are fetched without emitting
// per-node $ld:ai:usage:agent-config events; node trackers include the graph key.
func (c *Client) AgentGraph(
	graphKey string,
	context ldcontext.Context,
	variables map[string]interface{},
) AgentGraphDefinition {
	_ = c.sdk.TrackMetric(usageAgentGraph, context, 1, ldvalue.String(graphKey))

	defaultFlagValue := ldvalue.ObjectBuild().Set("root", ldvalue.String("")).Build()
	flagValue, err := c.sdk.JSONVariation(graphKey, context, defaultFlagValue)
	if err != nil {
		c.logConfigWarning(graphKey, "agent graph evaluation failed: %v", err)
		return newDisabledAgentGraphDefinition(disabledGraphFlagValue(), graphKey)
	}

	parsed := parseGraphFlagValue(flagValue)
	disabled := newDisabledAgentGraphDefinition(parsed, graphKey)

	if !parsed.enabled {
		c.logConfigWarning(graphKey, "agent graph is disabled")
		return disabled
	}
	if parsed.root == "" {
		c.logConfigWarning(graphKey, "agent graph has no root node")
		return disabled
	}

	allKeys := collectAllKeys(parsed)
	reachable := collectReachableKeys(parsed)
	for key := range allKeys {
		if _, ok := reachable[key]; !ok {
			c.logConfigWarning(graphKey, "agent graph has unconnected node %q", key)
			return disabled
		}
	}

	configs := make(map[string]AIAgentConfig, len(allKeys))
	nodeDefault := NewAIAgentConfigDefault().Disabled()
	for key := range allKeys {
		cfg := c.evaluateAgentConfig(key, context, nodeDefault, variables, graphKey, true)
		if !cfg.Enabled() {
			c.logConfigWarning(graphKey, "agent config %q in graph is not enabled or could not be fetched", key)
			return disabled
		}
		configs[key] = cfg
	}

	nodes, nodeKeys := buildGraphNodes(parsed, configs)
	return AgentGraphDefinition{
		enabled:      true,
		flagValue:    parsed,
		nodes:        nodes,
		nodeKeys:     nodeKeys,
		graphKey:     graphKey,
		variationKey: parsed.variationKey,
		version:      parsed.version,
	}
}
