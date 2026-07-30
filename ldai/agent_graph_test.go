package ldai

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldlogtest"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
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

func newTestGraphDef(flag graphFlagValue, configs map[string]AIAgentConfig) AgentGraphDefinition {
	nodes, keys := buildGraphNodes(flag, configs)
	return AgentGraphDefinition{
		enabled:   true,
		flagValue: flag,
		nodes:     nodes,
		nodeKeys:  keys,
		graphKey:  "g",
		version:   flag.version,
	}
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
	def := newTestGraphDef(flag, configs)

	assert.True(t, def.Enabled())
	require.NotNil(t, def.RootNode())
	assert.Equal(t, "a", def.RootNode().Key())
	assert.Equal(t, []string{"b", "c"}, edgeKeys(def.RootNode().Edges()))

	children := def.GetChildNodes("a")
	require.Len(t, children, 2)
	assert.Equal(t, []string{"b", "c"}, nodeKeys(children))

	parents := def.GetParentNodes("d")
	assert.Equal(t, []string{"b", "c"}, nodeKeys(parents))

	terminals := def.TerminalNodes()
	require.Len(t, terminals, 1)
	assert.Equal(t, "d", terminals[0].Key())
	assert.True(t, terminals[0].IsTerminal())

	t.Run("traverse visits each node once in topological order", func(t *testing.T) {
		var order []string
		def.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return node.Key() + "-done"
		}, nil)
		assert.Equal(t, []string{"a", "b", "c", "d"}, order)
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
		assert.Equal(t, []string{"d", "b", "c", "a"}, order)
	})

	t.Run("unequal-length convergence forward is topological", func(t *testing.T) {
		// r -> a -> d
		// r -> b -> c -> d
		// Topological order visits c before d (d depends on c).
		convFlag := graphFlagValue{
			root:    "r",
			enabled: true,
			edges: map[string][]GraphEdge{
				"r": {{key: "a"}, {key: "b"}},
				"a": {{key: "d"}},
				"b": {{key: "c"}},
				"c": {{key: "d"}},
			},
		}
		convConfigs := map[string]AIAgentConfig{
			"r": {aiConfigBase: aiConfigBase{key: "r", enabled: true}},
			"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
			"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
			"c": {aiConfigBase: aiConfigBase{key: "c", enabled: true}},
			"d": {aiConfigBase: aiConfigBase{key: "d", enabled: true}},
		}
		convDef := newTestGraphDef(convFlag, convConfigs)

		var order []string
		convDef.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		require.Equal(t, []string{"r", "a", "b", "c", "d"}, order)
	})

	t.Run("unequal-length convergence reverse is topological root-last", func(t *testing.T) {
		convFlag := graphFlagValue{
			root:    "r",
			enabled: true,
			edges: map[string][]GraphEdge{
				"r": {{key: "a"}, {key: "b"}},
				"a": {{key: "d"}},
				"b": {{key: "c"}},
				"c": {{key: "d"}},
			},
		}
		convConfigs := map[string]AIAgentConfig{
			"r": {aiConfigBase: aiConfigBase{key: "r", enabled: true}},
			"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
			"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
			"c": {aiConfigBase: aiConfigBase{key: "c", enabled: true}},
			"d": {aiConfigBase: aiConfigBase{key: "d", enabled: true}},
		}
		convDef := newTestGraphDef(convFlag, convConfigs)

		var order []string
		convDef.ReverseTraverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		require.Equal(t, "r", order[len(order)-1])
		assert.ElementsMatch(t, []string{"r", "a", "b", "c", "d"}, order)
		// Every non-root node appears before each of its parents.
		assert.Less(t, indexOf(order, "d"), indexOf(order, "a"))
		assert.Less(t, indexOf(order, "d"), indexOf(order, "c"))
		assert.Less(t, indexOf(order, "c"), indexOf(order, "b"))
		assert.Less(t, indexOf(order, "a"), indexOf(order, "r"))
		assert.Less(t, indexOf(order, "b"), indexOf(order, "r"))
	})

	t.Run("traversal order is deterministic across runs", func(t *testing.T) {
		// Multi-terminal, multi-parent graph:
		// r -> a -> t1
		// r -> b -> t2
		// r -> c -> t1
		detFlag := graphFlagValue{
			root:    "r",
			enabled: true,
			edges: map[string][]GraphEdge{
				"r": {{key: "a"}, {key: "b"}, {key: "c"}},
				"a": {{key: "t1"}},
				"b": {{key: "t2"}},
				"c": {{key: "t1"}},
			},
		}
		detConfigs := map[string]AIAgentConfig{
			"r":  {aiConfigBase: aiConfigBase{key: "r", enabled: true}},
			"a":  {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
			"b":  {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
			"c":  {aiConfigBase: aiConfigBase{key: "c", enabled: true}},
			"t1": {aiConfigBase: aiConfigBase{key: "t1", enabled: true}},
			"t2": {aiConfigBase: aiConfigBase{key: "t2", enabled: true}},
		}
		detDef := newTestGraphDef(detFlag, detConfigs)

		assert.Equal(t, []string{"a", "c"}, nodeKeys(detDef.GetParentNodes("t1")))
		assert.Equal(t, []string{"t1", "t2"}, nodeKeys(detDef.TerminalNodes()))

		var expectedForward, expectedReverse []string
		for i := 0; i < 50; i++ {
			var forward, reverse []string
			detDef.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
				forward = append(forward, node.Key())
				return nil
			}, nil)
			detDef.ReverseTraverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
				reverse = append(reverse, node.Key())
				return nil
			}, nil)
			if i == 0 {
				expectedForward = forward
				expectedReverse = reverse
			} else {
				assert.Equal(t, expectedForward, forward)
				assert.Equal(t, expectedReverse, reverse)
			}
		}
		assert.Equal(t, []string{"r", "a", "b", "c", "t1", "t2"}, expectedForward)
		assert.Equal(t, []string{"t1", "a", "c", "t2", "b", "r"}, expectedReverse)
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
		cycleDef := newTestGraphDef(cycleFlag, cycleConfigs)
		var order []string
		cycleDef.Traverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		assert.Equal(t, []string{"a", "b"}, order)
	})

	t.Run("reverse traverse pure cycle visits all root last", func(t *testing.T) {
		cycleFlag := graphFlagValue{
			root:    "a",
			enabled: true,
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}},
				"b": {{key: "a"}}, // no terminal node exists
			},
		}
		cycleConfigs := map[string]AIAgentConfig{
			"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
			"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
		}
		cycleDef := newTestGraphDef(cycleFlag, cycleConfigs)
		var order []string
		cycleDef.ReverseTraverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		assert.Equal(t, []string{"b", "a"}, order)
	})

	t.Run("reverse traverse single-node visits root", func(t *testing.T) {
		singleFlag := graphFlagValue{
			root:    "a",
			enabled: true,
			edges:   map[string][]GraphEdge{},
		}
		singleConfigs := map[string]AIAgentConfig{
			"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
		}
		singleDef := newTestGraphDef(singleFlag, singleConfigs)
		var order []string
		singleDef.ReverseTraverse(func(node *AgentGraphNode, _ map[string]interface{}) interface{} {
			order = append(order, node.Key())
			return nil
		}, nil)
		assert.Equal(t, []string{"a"}, order)
	})

	t.Run("traverse does not mutate caller initialContext", func(t *testing.T) {
		initial := map[string]interface{}{"sentinel": "keep"}
		def.Traverse(func(node *AgentGraphNode, ctx map[string]interface{}) interface{} {
			assert.Equal(t, "keep", ctx["sentinel"])
			return node.Key() + "-done"
		}, initial)
		assert.Equal(t, map[string]interface{}{"sentinel": "keep"}, initial)
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

func TestAgentGraphCanonicalTraversalVectors(t *testing.T) {
	type vector struct {
		id              string
		root            string
		edges           map[string][]GraphEdge
		forward         []string
		reverse         []string
		forwardContext  map[string][]string
		reverseContext  map[string][]string
	}

	// Shared context sets for G2 / G2b (only visit order differs).
	g2ForwardCtx := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"a"},
		"d": {"a", "c"},
		"e": {"a", "b", "c", "d"},
	}
	g2ReverseCtx := map[string][]string{
		"e": {},
		"b": {"e"},
		"d": {"e"},
		"c": {"d", "e"},
		"a": {"b", "c", "d", "e"},
	}

	vectors := []vector{
		{
			id:      "G1",
			root:    "a",
			edges:   map[string][]GraphEdge{"a": {{key: "b"}}, "b": {{key: "c"}}},
			forward: []string{"a", "b", "c"},
			reverse: []string{"c", "b", "a"},
			forwardContext: map[string][]string{
				"a": {},
				"b": {"a"},
				"c": {"a", "b"},
			},
			reverseContext: map[string][]string{
				"c": {},
				"b": {"c"},
				"a": {"b", "c"},
			},
		},
		{
			id: "G2",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}, {key: "c"}},
				"c": {{key: "d"}},
				"d": {{key: "e"}},
				"b": {{key: "e"}},
			},
			forward:        []string{"a", "b", "c", "d", "e"},
			reverse:        []string{"e", "b", "d", "c", "a"},
			forwardContext: g2ForwardCtx,
			reverseContext: g2ReverseCtx,
		},
		{
			id: "G2b",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "c"}, {key: "b"}},
				"c": {{key: "d"}},
				"d": {{key: "e"}},
				"b": {{key: "e"}},
			},
			forward:        []string{"a", "c", "b", "d", "e"},
			reverse:        []string{"e", "b", "d", "c", "a"},
			forwardContext: g2ForwardCtx,
			reverseContext: g2ReverseCtx,
		},
		{
			id: "G3",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}, {key: "c"}},
				"b": {{key: "d"}},
				"c": {{key: "d"}},
			},
			forward: []string{"a", "b", "c", "d"},
			reverse: []string{"d", "b", "c", "a"},
			forwardContext: map[string][]string{
				"a": {},
				"b": {"a"},
				"c": {"a"},
				"d": {"a", "b", "c"},
			},
			reverseContext: map[string][]string{
				"d": {},
				"b": {"d"},
				"c": {"d"},
				"a": {"b", "c", "d"},
			},
		},
		{
			id: "G4",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "n"}},
				"n": {{key: "m"}, {key: "t"}},
				"m": {{key: "t"}},
			},
			forward: []string{"a", "n", "m", "t"},
			reverse: []string{"t", "m", "n", "a"},
			forwardContext: map[string][]string{
				"a": {},
				"n": {"a"},
				"m": {"a", "n"},
				"t": {"a", "m", "n"},
			},
			reverseContext: map[string][]string{
				"t": {},
				"m": {"t"},
				"n": {"m", "t"},
				"a": {"m", "n", "t"},
			},
		},
		{
			id: "G5",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}, {key: "c"}},
				"b": {{key: "d"}},
			},
			forward: []string{"a", "b", "c", "d"},
			reverse: []string{"c", "d", "b", "a"},
			forwardContext: map[string][]string{
				"a": {},
				"b": {"a"},
				"c": {"a"},
				"d": {"a", "b"},
			},
			reverseContext: map[string][]string{
				"c": {},
				"d": {},
				"b": {"d"},
				"a": {"b", "c", "d"},
			},
		},
		{
			id: "G6",
			root: "a",
			edges: map[string][]GraphEdge{
				"a": {{key: "b"}},
				"b": {{key: "c"}},
				"c": {{key: "b"}},
			},
			forward: []string{"a", "b", "c"},
			reverse: []string{"b", "c", "a"},
			forwardContext: map[string][]string{
				"a": {},
				"b": {"a"},
				"c": {"a", "b"},
			},
			reverseContext: map[string][]string{
				"b": {},
				"c": {"b"},
				"a": {"b", "c"},
			},
		},
	}

	for _, v := range vectors {
		t.Run(v.id, func(t *testing.T) {
			configs := make(map[string]AIAgentConfig)
			for key := range collectAllKeys(graphFlagValue{root: v.root, edges: v.edges}) {
				configs[key] = AIAgentConfig{aiConfigBase: aiConfigBase{key: key, enabled: true}}
			}
			def := newTestGraphDef(graphFlagValue{root: v.root, enabled: true, edges: v.edges}, configs)

			forwardOrder, forwardCtx := captureTraversal(def.Traverse)
			reverseOrder, reverseCtx := captureTraversal(def.ReverseTraverse)

			assert.Equal(t, v.forward, forwardOrder, "forward order")
			assert.Equal(t, v.reverse, reverseOrder, "reverse order")
			assertScopedContexts(t, "forward", v.forwardContext, forwardCtx)
			assertScopedContexts(t, "reverse", v.reverseContext, reverseCtx)
		})
	}
}

func TestAgentGraphSelfLoopExcludedFromOwnContext(t *testing.T) {
	flag := graphFlagValue{
		root:    "a",
		enabled: true,
		edges: map[string][]GraphEdge{
			"a": {{key: "b"}},
			"b": {{key: "b"}},
		},
	}
	configs := map[string]AIAgentConfig{
		"a": {aiConfigBase: aiConfigBase{key: "a", enabled: true}},
		"b": {aiConfigBase: aiConfigBase{key: "b", enabled: true}},
	}
	def := newTestGraphDef(flag, configs)

	_, forwardCtx := captureTraversal(def.Traverse)
	_, reverseCtx := captureTraversal(def.ReverseTraverse)

	assert.Equal(t, []string{"a"}, forwardCtx["b"])
	assert.NotContains(t, forwardCtx["b"], "b")
	assert.Equal(t, []string{}, reverseCtx["b"])
	assert.NotContains(t, reverseCtx["b"], "b")
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

	t.Run("blank graphKey returns disabled without usage event", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(nil)
		client, err := NewClient(sdk)
		require.NoError(t, err)
		before := len(sdk.events)

		graph := client.AgentGraph("  ", ctx, nil)
		assert.False(t, graph.Enabled())
		assert.Nil(t, graph.RootNode())
		assert.Nil(t, graph.CreateTracker())

		for _, e := range sdk.events[before:] {
			assert.NotEqual(t, usageAgentGraph, e.eventName)
		}
	})

	t.Run("CreateTracker fresh runIds and works on disabled graph", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{"_ldMeta":{"enabled":false},"root":"agent-a","edges":{}}`),
			"agent-a":  agentFlagJSON("agent-a", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)

		graph := client.AgentGraph("my-graph", ctx, nil)
		assert.False(t, graph.Enabled())

		tracker1 := graph.CreateTracker()
		tracker2 := graph.CreateTracker()
		require.NotNil(t, tracker1)
		require.NotNil(t, tracker2)
		assert.NotEqual(t, tracker1.ResumptionToken(), tracker2.ResumptionToken())

		before := len(sdk.events)
		require.NoError(t, tracker1.TrackInvocationFailure())
		require.Greater(t, len(sdk.events), before)
		last := sdk.events[len(sdk.events)-1]
		assert.Equal(t, graphInvocationFailure, last.eventName)
		assert.Equal(t, "my-graph", last.data.GetByKey("graphKey").StringValue())
	})

	t.Run("CreateTracker on enabled graph", func(t *testing.T) {
		sdk := newMultiFlagMockSDK(map[string][]byte{
			"my-graph": []byte(`{
				"_ldMeta": {"enabled": true, "variationKey": "gv1", "version": 2},
				"root": "agent-a",
				"edges": {}
			}`),
			"agent-a": agentFlagJSON("agent-a", true),
		})
		client, err := NewClient(sdk)
		require.NoError(t, err)

		graph := client.AgentGraph("my-graph", ctx, nil)
		require.True(t, graph.Enabled())
		tracker := graph.CreateTracker()
		require.NotNil(t, tracker)

		require.NoError(t, tracker.TrackInvocationSuccess())
		last := sdk.events[len(sdk.events)-1]
		assert.Equal(t, graphInvocationSuccess, last.eventName)
		assert.Equal(t, "gv1", last.data.GetByKey("variationKey").StringValue())
		assert.Equal(t, 2, last.data.GetByKey("version").IntValue())
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

func indexOf(order []string, key string) int {
	for i, k := range order {
		if k == key {
			return i
		}
	}
	return -1
}

// captureTraversal runs a Traverse/ReverseTraverse method and records visit order plus
// sorted dependency context keys (excluding any initial-context keys) per node.
func captureTraversal(
	run func(fn TraverseFunc, initialContext map[string]interface{}),
) (order []string, ctxKeys map[string][]string) {
	ctxKeys = make(map[string][]string)
	run(func(node *AgentGraphNode, ctx map[string]interface{}) interface{} {
		order = append(order, node.Key())
		keys := make([]string, 0, len(ctx))
		for k := range ctx {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ctxKeys[node.Key()] = keys
		return node.Key() + "-result"
	}, nil)
	return order, ctxKeys
}

func assertScopedContexts(t *testing.T, direction string, expected, actual map[string][]string) {
	t.Helper()
	for node, want := range expected {
		got := actual[node]
		if got == nil {
			got = []string{}
		}
		assert.Equal(t, want, got, "%s context for node %q", direction, node)
	}
}
