package ldai

import (
	"context"
	"fmt"
	"regexp"

	"github.com/launchdarkly/go-server-sdk-ai/ldai/datamodel"

	"github.com/alexkappa/mustache"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// Defines the Mustache variable name used to access the provided context.
const ldContextVariable = "ldctx"

// JudgePlaceholderMessageHistory and JudgePlaceholderResponseToEvaluate are the literal placeholder
// strings shared between JudgeConfig (pass 1) and Judge.buildMessages (pass 2). Both must use the
// same values or substitution silently fails.
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
)

// modeAgent is the AI Config mode expected by AgentConfig and AgentConfigs.
const modeAgent = "agent"

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

// trackConfigUsage emits the usage metric for a single-config retrieval (completion, judge, or
// agent). The metric value is always 1 and the payload carries the config key.
func (c *Client) trackConfigUsage(eventName, key string, context ldcontext.Context) {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(eventName, context, 1, data)
}

// resolveMode returns the effective mode, preferring the metadata mode over the top-level field. An
// empty result means the mode is unspecified.
func resolveMode(metaMode, topMode string) string {
	if metaMode != "" {
		return metaMode
	}
	return topMode
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
	c.trackConfigUsage(usageCompletionConfig, key, context)
	return c.evaluateConfig(key, context, defaultValue, variables, "")
}

// CreateTracker reconstructs a Tracker from a resumption token and the given context.
// This delegates to TrackerFromResumptionToken. See that function for details.
func (c *Client) CreateTracker(token string, context ldcontext.Context) (*Tracker, error) {
	return TrackerFromResumptionToken(token, c.sdk, context)
}

// returnDefault sets a tracker factory on a copy of def (so CreateTracker always works) and returns
// the resulting Config verbatim. Used by evaluateConfig when the served value cannot be parsed or
// interpolated at all; the ordinary failure path interpolates the default instead.
func (c *Client) returnDefault(key string, context ldcontext.Context, def Config) Config {
	def.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, def.VariationKey(), def.Version(), context, &def, c.logger)
	}
	return def
}

// evaluateConfig fetches and interpolates an AI Config without emitting any metric. Callers
// (CompletionConfig, JudgeConfig, agentConfig) are meant to emit their own usage metric before
// calling this; the batch agentConfigs path intentionally emits a single aggregate metric instead.
//
// When expectedMode is non-empty, a successfully retrieved config whose mode does not match is
// rejected and replaced with a disabled config. A caller's default is interpolated like a served
// config but is never mode-validated.
func (c *Client) evaluateConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
	expectedMode string,
) Config {
	result, err := c.sdk.JSONVariation(key, context, defaultValue.AsLdValue())
	// On failure the SDK returns the serialized default in result; it is parsed like a served config
	// but never mode-validated.
	isDefault := err != nil

	// The spec requires the config to at least be an object (although all properties are optional, so it may be an
	// empty object.)
	if result.Type() != ldvalue.ObjectType {
		c.logConfigWarning(key, "unmarshalling failed, expected JSON object but got %s", result.Type().String())
		return c.returnDefault(key, context, defaultValue)
	}

	// Each field is parsed independently from the served value, so a malformed field is skipped
	// rather than discarding the whole config.
	meta := parseMeta(result.GetByKey("_ldMeta"))
	topMode := result.GetByKey("mode").StringValue()

	// An unspecified mode is not a mismatch (covers legacy payloads), and a caller's default is never
	// rejected for its mode.
	if servedMode := resolveMode(meta.Mode, topMode); !isDefault && expectedMode != "" &&
		servedMode != "" && servedMode != expectedMode {
		c.logConfigWarning(key, "mode mismatch: expected %q but the config is %q; returning a disabled config",
			expectedMode, servedMode)
		return c.disabledModeMismatch(key, context, meta, expectedMode)
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

	messages, err := c.interpolateMessages(key, result.GetByKey("messages"), mergedVariables)
	if err != nil {
		return c.returnDefault(key, context, defaultValue)
	}

	instructions, err := c.interpolateInstructions(key, result.GetByKey("instructions"), mergedVariables)
	if err != nil {
		return c.returnDefault(key, context, defaultValue)
	}

	// Parsed values are freshly allocated and unaliased, so they are assigned without copying.
	modelVal := result.GetByKey("model")
	cfg := Config{c: datamodel.Config{
		Meta: meta,
		Mode: topMode,
		Model: datamodel.Model{
			Name:       modelVal.GetByKey("name").StringValue(),
			Parameters: objectToValueMap(modelVal.GetByKey("parameters")),
			Custom:     objectToValueMap(modelVal.GetByKey("custom")),
		},
		Provider:             datamodel.Provider{Name: result.GetByKey("provider").GetByKey("name").StringValue()},
		Messages:             messages,
		Instructions:         instructions,
		Tools:                c.resolveTools(key, result),
		EvaluationMetricKey:  result.GetByKey("evaluationMetricKey").StringValue(),
		EvaluationMetricKeys: parseStringArray(result.GetByKey("evaluationMetricKeys")),
		JudgeConfiguration:   parseJudgeConfiguration(result.GetByKey("judgeConfiguration")),
	}}

	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.VariationKey(), cfg.Version(), context, &cfg, c.logger)
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

// JudgeConfig retrieves a Judge AI Config and interpolates its message templates. The reserved
// variables message_history and response_to_evaluate are preserved as literal placeholders for
// substitution by Judge.buildMessages during evaluation.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned Config to obtain a Tracker.
func (c *Client) JudgeConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	c.trackConfigUsage(usageJudgeConfig, key, context)

	// Extend variables with reserved judge placeholders
	extendedVariables := make(map[string]interface{})
	for k, v := range variables {
		// Warn if user tries to override reserved variables
		if k == "message_history" || k == "response_to_evaluate" {
			c.logger.Warnf("AI Config '%s': variable '%s' is reserved by judge and will be ignored", key, k)
			continue
		}
		extendedVariables[k] = v
	}

	// Inject reserved variables as literal placeholder strings
	// These will be preserved through the first interpolation and resolved during Judge.Evaluate()
	extendedVariables["message_history"] = JudgePlaceholderMessageHistory
	extendedVariables["response_to_evaluate"] = JudgePlaceholderResponseToEvaluate

	return c.evaluateConfig(key, context, defaultValue, extendedVariables, "")
}

// resolveTools extracts the tools from a served config value, mirroring the Python SDK's
// _resolve_tools. A present top-level "tools" object is authoritative (even when empty): each entry
// becomes a tool whose name defaults to its key, and non-object entries are skipped with a warning.
// Only when no top-level "tools" key exists does it fall back to the model parameters' tools array,
// where each entry must carry a name. Returns nil when the config defines no tools.
func (c *Client) resolveTools(key string, served ldvalue.Value) map[string]datamodel.Tool {
	if toolsVal, ok := served.TryGetByKey("tools"); ok {
		if toolsVal.Type() != ldvalue.ObjectType {
			return nil
		}
		tools := make(map[string]datamodel.Tool)
		for _, name := range toolsVal.Keys(nil) {
			item := toolsVal.GetByKey(name)
			if item.Type() != ldvalue.ObjectType {
				c.logConfigWarning(key, "skipping tool %q: expected an object", name)
				continue
			}
			tools[name] = toolFromValue(item, name)
		}
		return emptyToNil(tools)
	}

	raw := served.GetByKey("model").GetByKey("parameters").GetByKey("tools")
	if raw.Type() != ldvalue.ArrayType {
		return nil
	}
	tools := make(map[string]datamodel.Tool)
	for i := 0; i < raw.Count(); i++ {
		item := raw.GetByIndex(i)
		if item.Type() != ldvalue.ObjectType {
			c.logConfigWarning(key, "skipping tool entry at index %d: expected an object", i)
			continue
		}
		name := item.GetByKey("name").StringValue()
		if name == "" {
			c.logConfigWarning(key, "skipping tool entry at index %d: missing name", i)
			continue
		}
		tools[name] = toolFromValue(item, name)
	}
	return emptyToNil(tools)
}

// toolFromValue builds a Tool from a config value, defaulting the name to fallbackName when the
// entry has no explicit name (as the top-level tools map keys its entries by name).
func toolFromValue(item ldvalue.Value, fallbackName string) datamodel.Tool {
	name := fallbackName
	if explicit := item.GetByKey("name").StringValue(); explicit != "" {
		name = explicit
	}
	return datamodel.Tool{
		Name:             name,
		Description:      item.GetByKey("description").StringValue(),
		Type:             item.GetByKey("type").StringValue(),
		Parameters:       item.GetByKey("parameters"),
		CustomParameters: item.GetByKey("customParameters"),
	}
}

// emptyToNil returns nil for an empty tools map so callers can treat "no tools" uniformly (parity
// with the Python SDK's `tools or None`).
func emptyToNil(tools map[string]datamodel.Tool) map[string]datamodel.Tool {
	if len(tools) == 0 {
		return nil
	}
	return tools
}

// parseMeta reads the config metadata from the _ldMeta value. The version pointer is set only when
// a numeric version is present, so an absent version defaults to 1 via Config.Version.
func parseMeta(metaVal ldvalue.Value) datamodel.Meta {
	meta := datamodel.Meta{
		VariationKey: metaVal.GetByKey("variationKey").StringValue(),
		Enabled:      metaVal.GetByKey("enabled").BoolValue(),
		Mode:         metaVal.GetByKey("mode").StringValue(),
	}
	if v, ok := metaVal.TryGetByKey("version"); ok && v.Type() == ldvalue.NumberType {
		n := v.IntValue()
		meta.Version = &n
	}
	return meta
}

// parseStringArray returns the string entries of an array value, skipping non-strings. A non-array
// value yields nil.
func parseStringArray(arr ldvalue.Value) []string {
	if arr.Type() != ldvalue.ArrayType {
		return nil
	}
	var out []string
	for i := 0; i < arr.Count(); i++ {
		if item := arr.GetByIndex(i); item.Type() == ldvalue.StringType {
			out = append(out, item.StringValue())
		}
	}
	return out
}

// parseJudgeConfiguration reads the judge configuration, skipping judge entries that are not objects
// or whose key is not a string or sampling rate is not a number (rather than silently coercing them
// to "" / 0). Returns nil when no valid judges are defined.
func parseJudgeConfiguration(jcVal ldvalue.Value) *datamodel.JudgeConfiguration {
	judgesVal := jcVal.GetByKey("judges")
	if jcVal.Type() != ldvalue.ObjectType || judgesVal.Type() != ldvalue.ArrayType {
		return nil
	}
	var judges []datamodel.Judge
	for i := 0; i < judgesVal.Count(); i++ {
		j := judgesVal.GetByIndex(i)
		key := j.GetByKey("key")
		rate := j.GetByKey("samplingRate")
		if j.Type() != ldvalue.ObjectType || key.Type() != ldvalue.StringType || rate.Type() != ldvalue.NumberType {
			continue
		}
		judges = append(judges, datamodel.Judge{Key: key.StringValue(), SamplingRate: rate.Float64Value()})
	}
	if len(judges) == 0 {
		return nil
	}
	return &datamodel.JudgeConfiguration{Judges: judges}
}

// objectToValueMap returns a copy of an object value as a map, or nil when the value is not an
// object.
func objectToValueMap(v ldvalue.Value) map[string]ldvalue.Value {
	if v.Type() != ldvalue.ObjectType {
		return nil
	}
	return v.AsValueMap().AsMap()
}

// interpolateMessages returns the messages with their content templates interpolated, used only when
// messagesVal is an array whose every entry is an object (otherwise they are dropped with a warning,
// matching the Python SDK). A non-nil error means a template was malformed and the caller should
// return the default.
func (c *Client) interpolateMessages(
	key string,
	messagesVal ldvalue.Value,
	variables map[string]interface{},
) ([]datamodel.Message, error) {
	if messagesVal.Type() != ldvalue.ArrayType {
		if messagesVal.Type() != ldvalue.NullType {
			c.logConfigWarning(key, "skipping messages: expected an array")
		}
		return nil, nil
	}
	if !allObjects(messagesVal) {
		c.logConfigWarning(key, "skipping messages: every entry must be an object")
		return nil, nil
	}
	var messages []datamodel.Message
	for i := 0; i < messagesVal.Count(); i++ {
		entry := messagesVal.GetByIndex(i)
		content, err := interpolateTemplate(entry.GetByKey("content").StringValue(), variables)
		if err != nil {
			c.logConfigWarning(key, "malformed message at index %d: %v", i, err)
			return nil, err
		}
		messages = append(messages, datamodel.Message{
			Content: content,
			Role:    datamodel.Role(entry.GetByKey("role").StringValue()),
		})
	}
	return messages, nil
}

// interpolateInstructions returns the interpolated agent instructions, or "" when instructionsVal is
// not a string. A non-nil error means the template was malformed and the caller should return the
// default.
func (c *Client) interpolateInstructions(
	key string,
	instructionsVal ldvalue.Value,
	variables map[string]interface{},
) (string, error) {
	if instructionsVal.Type() != ldvalue.StringType {
		return "", nil
	}
	instructions, err := interpolateTemplate(instructionsVal.StringValue(), variables)
	if err != nil {
		c.logConfigWarning(key, "malformed instructions: %v", err)
		return "", err
	}
	return instructions, nil
}

// allObjects reports whether every entry of an array value is a JSON object.
func allObjects(arr ldvalue.Value) bool {
	for i := 0; i < arr.Count(); i++ {
		if arr.GetByIndex(i).Type() != ldvalue.ObjectType {
			return false
		}
	}
	return true
}

// AgentConfigRequest describes one agent configuration to retrieve via AgentConfigs.
type AgentConfigRequest struct {
	// Key is the agent configuration key.
	Key string

	// DefaultValue is returned when the configuration cannot be evaluated.
	DefaultValue Config

	// Variables are used to interpolate the agent's instruction template.
	Variables map[string]interface{}
}

// AgentConfig retrieves an agent AI Config and interpolates its instruction template using the
// provided variables. Returns the default value if the config cannot be evaluated, and a disabled
// config if the retrieved config's mode is not "agent". The ctx parameter is accepted per the Go
// SDK convention for new methods; evaluation itself does not block.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned Config to obtain a Tracker.
func (c *Client) AgentConfig(
	ctx context.Context,
	key string,
	ldctx ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	c.trackConfigUsage(usageAgentConfig, key, ldctx)
	return c.agentConfig(ctx, key, ldctx, defaultValue, variables)
}

// AgentConfigs retrieves multiple agent AI Configs, each with its own default value and
// interpolation variables, and returns a map from each request's key to its configuration. A single
// aggregate usage metric is emitted for the batch. ctx cancellation causes remaining evaluations to
// return their defaults. If two requests share a key, the later one wins (matching the Python SDK).
func (c *Client) AgentConfigs(
	ctx context.Context,
	requests []AgentConfigRequest,
	ldctx ldcontext.Context,
) map[string]Config {
	count := len(requests)
	data := ldvalue.ObjectBuild().Set("count", ldvalue.Int(count)).Build()
	_ = c.sdk.TrackMetric(usageAgentConfigs, ldctx, float64(count), data)

	agents := make(map[string]Config, count)
	for _, req := range requests {
		agents[req.Key] = c.agentConfig(ctx, req.Key, ldctx, req.DefaultValue, req.Variables)
	}
	return agents
}

// agentConfig evaluates a single agent config without emitting a usage metric. Mode validation
// happens inside evaluateConfig, which returns a disabled config on a mismatch.
func (c *Client) agentConfig(
	ctx context.Context,
	key string,
	ldctx ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	if ctx.Err() != nil {
		c.logConfigWarning(key, "context done before evaluation: %v", ctx.Err())
		return c.returnDefault(key, ldctx, defaultValue)
	}
	return c.evaluateConfig(key, ldctx, defaultValue, variables, modeAgent)
}

// disabledModeMismatch returns a disabled Config used when a served config's mode does not match
// the mode expected by the retrieval method. It preserves the served metadata (variationKey,
// version) for tracking, stamps the expected mode, and drops all config content so the rejected
// messages/instructions/tools cannot leak through the returned config or its tracker.
func (c *Client) disabledModeMismatch(
	key string,
	context ldcontext.Context,
	servedMeta datamodel.Meta,
	expectedMode string,
) Config {
	meta := servedMeta
	meta.Enabled = false
	meta.Mode = expectedMode
	cfg := Config{c: datamodel.Config{Mode: expectedMode, Meta: meta}}
	cfg.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, cfg.VariationKey(), cfg.Version(), context, &cfg, c.logger)
	}
	return cfg
}
