package ldai

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldlogtest"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
)

// multiFlagMockSDK returns per-key JSON variations for agent graph integration tests.
type multiFlagMockSDK struct {
	log    *ldlogtest.MockLog
	flags  map[string][]byte
	events []mockEvent
}

func newMultiFlagMockSDK(flags map[string][]byte) *multiFlagMockSDK {
	return &multiFlagMockSDK{flags: flags, log: ldlogtest.NewMockLog(), events: []mockEvent{}}
}

func (m *multiFlagMockSDK) JSONVariation(
	key string,
	_ ldcontext.Context,
	defaultVal ldvalue.Value,
) (ldvalue.Value, error) {
	raw, ok := m.flags[key]
	if !ok {
		return defaultVal, nil
	}
	return ldvalue.Parse(raw), nil
}

func (m *multiFlagMockSDK) Loggers() interfaces.LDLoggers { return m.log.Loggers }

func (m *multiFlagMockSDK) TrackMetric(eventName string, context ldcontext.Context, metricValue float64, data ldvalue.Value) error {
	m.events = append(m.events, mockEvent{
		eventName:   eventName,
		context:     context,
		metricValue: metricValue,
		data:        data,
	})
	return nil
}

func agentFlagJSON(key string, enabled bool) []byte {
	enabledStr := "true"
	if !enabled {
		enabledStr = "false"
	}
	return []byte(fmt.Sprintf(`{
		"_ldMeta": {"enabled": %s, "mode": "agent", "variationKey": "v-%s", "version": 1},
		"instructions": "Instructions for %s",
		"model": {"name": "gpt-4"},
		"provider": {"name": "openai"}
	}`, enabledStr, key, key))
}

func TestParseGraphFlagValue(t *testing.T) {
	t.Run("non-object yields disabled", func(t *testing.T) {
		parsed := parseGraphFlagValue(ldvalue.String("nope"))
		assert.False(t, parsed.enabled)
		assert.Empty(t, parsed.root)
	})

	t.Run("meta defaults enabled true and version 1", func(t *testing.T) {
		parsed := parseGraphFlagValue(ldvalue.Parse([]byte(`{"root":"a","edges":{}}`)))
		assert.True(t, parsed.enabled)
		assert.Equal(t, 1, parsed.version)
		assert.Equal(t, "a", parsed.root)
	})

	t.Run("handoff preserved and empty handoff becomes nil", func(t *testing.T) {
		parsed := parseGraphFlagValue(ldvalue.Parse([]byte(`{
			"root": "a",
			"edges": {
				"a": [
					{"key": "b", "handoff": {"tool": "search"}},
					{"key": "c", "handoff": {}}
				]
			}
		}`)))
		require.Len(t, parsed.edges["a"], 2)
		assert.Equal(t, "b", parsed.edges["a"][0].Key())
		assert.Equal(t, "search", parsed.edges["a"][0].Handoff()["tool"].StringValue())
		assert.Nil(t, parsed.edges["a"][1].Handoff())
	})

	t.Run("malformed edges skipped", func(t *testing.T) {
		parsed := parseGraphFlagValue(ldvalue.Parse([]byte(`{
			"root": "a",
			"edges": {
				"a": [
					"not-an-object",
					{"noKey": true},
					{"key": "b"}
				],
				"bad": "not-array"
			}
		}`)))
		require.Len(t, parsed.edges["a"], 1)
		assert.Equal(t, "b", parsed.edges["a"][0].Key())
		_, hasBad := parsed.edges["bad"]
		assert.False(t, hasBad)
	})
}

func TestAgentGraphDefinitionAccessorsAndTraverse(t *testing.T) {
	// a -> b -> d
	// a -> c -> d  (diamond)
	flag := graphFlagValue{
		root:    "a",
		enabled: true,
		version: 1,
		edges: map[string][]GraphEdge{
			"a": {{key: "b"}, {key: "c"}},
			"b": {{key: "d"}},
			"c": {{key: "d"}},
		},
	}
	configs := map[string]AIAgentConfig{
		"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
		"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
		"c": {aiConfigBase: aiConfigBase{key: "c", enabled: true}},
		"d": {aiConfigBase: aiConfigBase{key: "d", enabled: true}},
	}
	def := AgentGraphDefinition{
		enabled:   true,
		flagValue: flag,
		nodes:     buildGraphNodes(flag, configs),
		graphKey:  "g",
		version:   1,
	}

	assert.True(t, def.Enabled())
	require.NotNil(t, def.RootNode())
	assert.Equal(t, "a", def.RootNode().Key())
	assert.Equal(t, []string{"b", "c"}, edgeKeys(def.RootNode().Edges()))

	children := def.GetChildNodes("a")
	require.Len(t, children, 2)
	assert.ElementsMatch(t, []string{"b", "c"}, nodeKeys(children))

	parents := def.GetParentNodes("d")
	assert.ElementsMatch(t, []string{"b", "c"}, nodeKeys(parents))

	terminals := def.TerminalNodes()
	require.Len(t, terminals, 1)
	assert.Equal(t, "d", terminals[0].Key())
	assert.True(t, terminals[0].IsTerminal())

	t.Run("traverse BFS visits each node once", func(t *testing.T) {
		var order []string
		ctx := map[string]interface{}{}
		def.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return node.Key() + "-done"
		}, ctx)
		assert.Equal(t, "a", order[0])
		assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, order)
		assert.Equal(t, "a-done", ctx["a"])
		assert.Equal(t, "d-done", ctx["d"])
		// d appears once despite diamond.
		assert.Equal(t, 1, countKey(order, "d"))
	})

	t.Run("reverse traverse processes root last", func(t *testing.T) {
		var order []string
		def.ReverseTraverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		require.NotEmpty(t, order)
		assert.Equal(t, "a", order[len(order)-1])
		assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, order)
	})

	t.Run("cycle safe traverse", func(t *testing.T) {
		cycleFlag := graphFlagValue{
			root:    "a",
			enabled: true,
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}},
				"b": {{key: "a"}},
			},
		}
		cycleConfigs := map[string]AIAgentConfig{
			"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
			"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
		}
		cycleDef := AgentGraphDefinition{
			enabled:   true,
			flagValue: cycleFlag,
			nodes:     buildGraphNodes(cycleFlag, cycleConfigs),
		}
		var order []string
		cycleDef.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		assert.Equal(t, []string{"a", "b"}, order)
	})

	t.Run("disabled graph traversals are no-ops", func(t *testing.T) {
		disabled := newDisabledAgentGraphDefinition(flag, "g")
		assert.False(t, disabled.Enabled())
		assert.Nil(t, disabled.RootNode())
		called := false
		disabled.Traverse(func(_ *AgentGraphNode, _ map[string]interface{}) interface{} {
			called = true
			return nil
		}, nil)
		disabled.ReverseTraverse(func(_ *AgentGraphNode, _ map[string]interface{}) interface{} {
			called = true
			return nil
		}, nil)
		assert.False(t, called)
	})
}

func TestAgentGraphClient(t *testing.T) {
	ctx := ldcontext.New("user")

	t.Run("happy path enabled graph", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{
				"_ldMeta": {"enabled": true, "variationKey": "gv1", "version": 2},
				"root": "agent-a",
				"edges": {
					"agent-a": [{"key": "agent-b", "handoff": {"tool": "search"}}],
					"agent-b": []
				}
			}`),
			"agent-a": agentFlagJSON("agent-a", true),
			"agent-b": agentFlagJSON("agent-b", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)

		graph := client.AgentGraph("my-graph", ctx, nil)
		require.True(t, graph.Enabled())
		require.NotNil(t, graph.RootNode())
		assert.Equal(t, "agent-a", graph.RootNode().Key())
		assert.Equal(t, "Instructions for agent-a", graph.RootNode().Config().Instructions())
		require.Len(t, graph.RootNode().Edges(), 1)
		assert.Equal(t, "search", graph.RootNode().Edges()[0].Handoff()["tool"].StringValue())
		assert.True(t, graph.GetNode("agent-b").IsTerminal())
	})

	t.Run("emits usage agent-graph once and no agent-config events", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{"root":"agent-a","edges":{"agent-a":[{"key":"agent-b"}]}}`),
			"agent-a":  agentFlagJSON("agent-a", true),
			"agent-b":  agentFlagJSON("agent-b", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)
		_ = client.AgentGraph("my-graph", ctx, nil)

		var graphUsage, agentConfigUsage int
		for _, e := range sdk.events {
			switch e.eventName {
			case usageAgentGraph:
				graphUsage++
				assert.Equal(t, float64(1), e.metricValue)
				assert.Equal(t, "my-graph", e.data.StringValue())
			case usageAgentConfig:
				agentConfigUsage++
			}
		}
		assert.Equal(t, 1, graphUsage)
		assert.Equal(t, 0, agentConfigUsage)
	})

	t.Run("graphKey present on node trackers", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{"root":"agent-a","edges":{}}`),
			"agent-a":  agentFlagJSON("agent-a", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)

		graph := client.AgentGraph("my-graph", ctx, nil)
		require.True(t, graph.Enabled())
		tracker := graph.RootNode().Config().CreateTracker()
		require.NotNil(t, tracker)
		tracker.TrackSuccess()

		var found bool
		for _, e := range sdk.events {
			if e.eventName == "$ld:ai:generation:success" {
				assert.Equal(t, "my-graph", e.data.GetByKey("graphKey").StringValue())
				found = true
			}
		}
		assert.True(t, found)
	})

	t.Run("no-variables wrapper matches nil variables", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{"root":"agent-a","edges":{}}`),
			"agent-a":  agentFlagJSON("agent-a", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)

		g1 := client.AgentGraph("my-graph", ctx, nil)
		g2 := client.AgentGraphNoVariables("my-graph", ctx)
		assert.Equal(t, g1.Enabled(), g2.Enabled())
		assert.Equal(t, g1.RootNode().Key(), g2.RootNode().Key())
	})

	t.Run("blank graphKey returns disabled without usage event", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(nil)
		client, err := NewClient(sdk)
		require.NoError(t, err)
		before := len(sdk.events)

		graph := client.AgentGraph("  ", ctx, nil)
		assert.False(t, graph.Enabled())
		assert.Nil(t, graph.RootNode())

		for _, e := range sdk.events[before:] {
			assert.NotEqual(t, usageAgentGraph, e.eventName)
		}
	})

	validationCases := []struct {
		name  string
		flags map[string][]byte
	}{
		{
			name: "disabled meta",
			flags: map[string][]byte{
				"my-graph": []byte(`{"_ldMeta":{"enabled":false},"root":"agent-a","edges":{}}`),
				"agent-a":  agentFlagJSON("agent-a", true),
			},
		},
		{
			name: "missing root",
			flags: map[string][]byte{
				"my-graph": []byte(`{"_ldMeta":{"enabled":true},"edges":{}}`),
			},
		},
		{
			name: "orphan node",
			flags: map[string][]byte{
				"my-graph": []byte(`{
					"root": "agent-a",
					"edges": {
						"agent-a": [{"key": "agent-b"}],
						"orphan": [{"key": "agent-b"}]
					}
				}`),
				"agent-a": agentFlagJSON("agent-a", true),
				"agent-b": agentFlagJSON("agent-b", true),
				"orphan":  agentFlagJSON("orphan", true),
			},
		},
		{
			name: "disabled child",
			flags: map[string][]byte{
				"my-graph": []byte(`{"root":"agent-a","edges":{"agent-a":[{"key":"agent-b"}]}}`),
				"agent-a":  agentFlagJSON("agent-a", true),
				"agent-b":  agentFlagJSON("agent-b", false),
			},
		},
		{
			name: "unfetchable child",
			flags: map[string][]byte{
				"my-graph": []byte(`{"root":"agent-a","edges":{"agent-a":[{"key":"missing-agent"}]}}`),
				"agent-a":  agentFlagJSON("agent-a", true),
			},
		},
	}

	for _, tc := range validationCases {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			sdk := newMultiFlagMockSDK(tc.flags)
			client, err := NewClient(sdk)
			require.NoError(t, err)

			graph := client.AgentGraph("my-graph", ctx, map[string]interface{}{"x": "y"})
			assert.False(t, graph.Enabled())
			assert.Nil(t, graph.RootNode())
			assert.Empty(t, graph.TerminalNodes())

			called := false
			graph.Traverse(func(_ *AgentGraphNode, _ map[string]interface{}) interface{} {
				called = true
				return nil
			}, nil)
			assert.False(t, called)
		})
	}
}

func edgeKeys(edges []GraphEdge) []string {
	keys := make([]string, len(edges))
	for i, e := range edges {
		keys[i] = e.Key()
	}
	return keys
}

func nodeKeys(nodes []*AgentGraphNode) []string {
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		keys[i] = n.Key()
	}
	return keys
}

func countKey(order []string, key string) int {
	n := 0
	for _, k := range order {
		if k == key {
			n++
		}
	}
	return n
}
