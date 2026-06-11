package ldai

import (
	"encoding/json"
	"fmt"
	"regexp"
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
// strings used by legacy judge configs to mark where the evaluated conversation should be inserted.
// JudgeConfig preserves them through interpolation so the messages containing them can be detected
// and removed; see StripLegacyJudgeMessages.
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
	return c.evaluateConfig(key, context, defaultValue, variables, nil)
}

// CreateTracker reconstructs a Tracker from a resumption token and the given context.
// This delegates to TrackerFromResumptionToken. See that function for details.
func (c *Client) CreateTracker(token string, context ldcontext.Context) (*Tracker, error) {
	return TrackerFromResumptionToken(token, c.sdk, context)
}

// messageTransform is applied to a Config's messages before its tracker factory is created, so
// that trackers and the returned Config always observe the same message list.
type messageTransform func([]datamodel.Message) []datamodel.Message

// returnDefault sets a tracker factory on a copy of def (so CreateTracker always works) and
// returns the resulting Config. Used for all error-path returns in evaluateConfig.
func (c *Client) returnDefault(key string, context ldcontext.Context, def Config, transform messageTransform) Config {
	if transform != nil {
		def.c.Messages = transform(def.c.Messages)
	}
	def.trackerFactory = func() *Tracker {
		return newTracker(c.sdk, newRunID(), key, def.VariationKey(), def.Version(), context, &def, c.logger)
	}
	return def
}

// evaluateConfig fetches and interpolates an AI Config without emitting any metric.
// Callers (CompletionConfig, JudgeConfig) are meant to emit their own metric before calling this.
// If transform is non-nil it is applied to the resulting messages (including on default-value
// paths) before the tracker factory is created.
func (c *Client) evaluateConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
	transform messageTransform,
) Config {
	result, err := c.sdk.JSONVariation(key, context, defaultValue.AsLdValue())
	if err != nil {
		// The evaluation failed (e.g. flag not found, client not initialized), so the SDK returned the
		// default value. Return it as-is: interpolation is not applied to the default value's messages.
		return c.returnDefault(key, context, defaultValue, transform)
	}

	// The spec requires the config to at least be an object (although all properties are optional, so it may be an
	// empty object.)
	if result.Type() != ldvalue.ObjectType {
		c.logConfigWarning(key, "unmarshalling failed, expected JSON object but got %s", result.Type().String())
		return c.returnDefault(key, context, defaultValue, transform)
	}

	var parsed datamodel.Config
	if err := json.Unmarshal(result.AsRaw(), &parsed); err != nil {
		c.logConfigWarning(key, "unmarshalling failed: %v", err)
		return c.returnDefault(key, context, defaultValue, transform)
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

	builder := NewConfig().
		WithModelName(parsed.Model.Name).
		WithProviderName(parsed.Provider.Name).
		WithEnabled(parsed.Meta.Enabled).
		WithMode(parsed.Mode).
		WithEvaluationMetricKey(parsed.EvaluationMetricKey).
		WithEvaluationMetricKeys(parsed.EvaluationMetricKeys).
		WithJudgeConfiguration(parsed.JudgeConfiguration)

	for k, v := range parsed.Model.Parameters {
		builder.WithModelParam(k, v)
	}

	for k, v := range parsed.Model.Custom {
		builder.WithCustomModelParam(k, v)
	}

	for i, msg := range parsed.Messages {
		content, err := interpolateTemplate(msg.Content, mergedVariables)
		if err != nil {
			c.logConfigWarning(key,
				"malformed message at index %d: %v", i, err,
			)
			return c.returnDefault(key, context, defaultValue, transform)
		}
		builder.WithMessage(content, msg.Role)
	}

	cfg := builder.Build()
	cfg.c.Meta.VariationKey = parsed.Meta.VariationKey
	cfg.c.Meta.Version = parsed.Meta.Version
	if transform != nil {
		cfg.c.Messages = transform(cfg.c.Messages)
	}

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

// JudgeConfig retrieves a Judge AI Config and interpolates its message templates. Legacy judge
// configs contain template messages with the reserved {{message_history}} and
// {{response_to_evaluate}} placeholders; those messages are stripped from the returned config.
// New-style judge configs omit them entirely: the judge supplies the evaluated conversation as
// part of its evaluation input instead.
//
// To send analytic events to LaunchDarkly, call CreateTracker on the returned Config to obtain a Tracker.
func (c *Client) JudgeConfig(
	key string,
	context ldcontext.Context,
	defaultValue Config,
	variables map[string]interface{},
) Config {
	data := ldvalue.ObjectBuild().Set("configKey", ldvalue.String(key)).Build()
	_ = c.sdk.TrackMetric(usageJudgeConfig, context, 1, data)

	extendedVariables := make(map[string]interface{})
	for k, v := range variables {
		// Warn if user tries to override reserved variables
		if k == "message_history" || k == "response_to_evaluate" {
			c.logger.Warnf("AI Config '%s': variable '%s' is reserved by judge and will be ignored", key, k)
			continue
		}
		extendedVariables[k] = v
	}

	// Re-inject the reserved variables as their literal placeholders so they survive Mustache
	// interpolation in evaluateConfig. Without this, legacy templates like {{message_history}}
	// get rendered to empty strings and StripLegacyJudgeMessages below cannot detect them.
	extendedVariables["message_history"] = JudgePlaceholderMessageHistory
	extendedVariables["response_to_evaluate"] = JudgePlaceholderResponseToEvaluate

	// The strip runs inside evaluateConfig, before the tracker factory captures the config, so
	// trackers observe the same stripped messages as the returned Config.
	return c.evaluateConfig(key, context, defaultValue, extendedVariables, StripLegacyJudgeMessages)
}

// StripLegacyJudgeMessages returns messages with legacy judge template messages removed: any
// non-system message whose content contains the literal JudgePlaceholderMessageHistory or
// JudgePlaceholderResponseToEvaluate placeholder. Older judge configs used these messages to mark
// where the SDK should insert the evaluated conversation; new configs omit them and rely on the
// evaluation input built by the judge.
func StripLegacyJudgeMessages(messages []datamodel.Message) []datamodel.Message {
	result := make([]datamodel.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != datamodel.System &&
			(strings.Contains(msg.Content, JudgePlaceholderMessageHistory) ||
				strings.Contains(msg.Content, JudgePlaceholderResponseToEvaluate)) {
			continue
		}
		result = append(result, msg)
	}
	return result
}
