# Task 06 — Agent graph: model, nodes, definition, traversal, `AgentGraph` method

**Depends on:** 01 (typed configs, datamodel), 02 (tracker `graphKey`),
03 (agent config + internal `evaluateAgentConfig(..., graphKey)`).
**Blocks:** 07 (graph tracking).

## Objective

Implement agent graphs: the graph wire model, an `AgentGraphNode`, an
`AgentGraphDefinition` with breadth-first `Traverse` / `ReverseTraverse` and
relationship helpers, and the `AgentGraph` client method that fetches, builds,
and validates a graph (returning a disabled graph on any validation failure).

## Spec references

- AIGRAPH §1.1 — JSON protocol (`root` string, `edges` map: sourceKey →
  `[{key, handoff}]`)
- AIGRAPH §1.3 — `AgentGraphNode` (`GetKey`, `GetConfig` (`AIAgentConfig`),
  `GetEdges`, `IsTerminal`; holds full `AIAgentConfig`; refs parent graph)
- AIGRAPH §1.4 — `AgentGraphDefinition` (`_build_nodes`, `Enabled`,
  `GetChildNodes`, `GetParentNodes`, `TerminalNodes`, `RootNode`, `GetNode`,
  `GetConfig`, `CreateTracker` → `AIGraphTracker`, `Traverse`, `ReverseTraverse`)
- AIGRAPH §1.4.2.1 — when building nodes, pass the graph key as `graph_key` to
  the internal agent-config eval helper (so node trackers carry `graphKey`)
- AIGRAPH §1.5 — `agentGraph(graphKey, context, variables)`; validation rules
  (fetchable, single root, no unconnected nodes, all child configs fetchable);
  disabled state + DEBUG warnings on failure; usage event
  `$ld:ai:usage:agent-graph`

## Reference implementations

- .NET `Graph/`: `AgentGraphFlagValue.cs` (wire), `AgentGraphNode.cs`,
  `AgentGraphDefinition.cs`, `GraphEdge.cs`; `LdAiClient.cs` (`AgentGraph`),
  `Interfaces/ILdAiGraphClient.cs`.
- Java: `AgentGraphDefinition.java`, `AgentGraphNode.java`, `GraphEdge.java`,
  `internal/AgentGraphFlagValue.java`, `LDAIClientImpl.agentGraph`.

## Current Go state

None. `Client` has no graph methods; no graph datamodel.

## Implementation steps

1. **Wire model** (`ldai/datamodel`): `AgentGraphFlagValue` with `Meta`
   (`_ldMeta`), `Root string `json:"root"``, and `Edges map[string][]GraphEdge`
   where `GraphEdge` = `{ Key string `json:"key"`, Handoff map[string]ldvalue.Value `json:"handoff,omitempty"` }`.
2. **`AgentGraphNode`** (`ldai/graph.go` or `ldai/agent_graph.go`):
   - Holds `key`, `config AIAgentConfig`, `edges []GraphEdge`, and a pointer to
     the parent `AgentGraphDefinition`.
   - `Key()`, `Config()`, `Edges()`, `IsTerminal()` (true when no child edges).
3. **`AgentGraphDefinition`**:
   - Built via a `buildNodes(graph, context, variables, client)` helper
     (AIGRAPH 1.4.2). For each referenced config key, fetch the child
     `AIAgentConfig` via the internal `evaluateAgentConfig(..., graphKey=graphKey)`
     (AIGRAPH 1.4.2.1) so node trackers carry `graphKey`.
   - Properties/methods: `Enabled()`, `GetChildNodes(key)`,
     `GetParentNodes(key)`, `TerminalNodes()`, `RootNode()`, `GetNode(key)`,
     `GetConfig()`, `CreateTracker() *AIGraphTracker` (task 07),
     `Traverse(fn, initialCtx)`, `ReverseTraverse(fn, initialCtx)`.
   - `Traverse`: breadth-first from `root`; process a node only after all nodes
     at earlier depths are processed; pass each node + a **mutable** execution
     context map to `fn`; store `fn`'s return value in the execution context
     keyed by the node key (AIGRAPH 1.4.3 `traverse`). Go signature suggestion:
     `Traverse(fn func(node *AgentGraphNode, execCtx map[string]interface{}) interface{}, initial map[string]interface{})`.
   - `ReverseTraverse`: same but from terminal nodes up to root, honoring the
     longest path so deeper nodes are visited first (AIGRAPH 1.4.3
     `reverse_traverse`).
   - Guard against cycles (BFS with a visited set).
4. **`AgentGraph` client method** (AIGRAPH 1.5):
   - Signature: `AgentGraph(graphKey string, context ldcontext.Context,
     variables map[string]interface{}) AgentGraphDefinition` (also a no-variables
     convenience matching Java's default is nice-to-have).
   - Emit `$ld:ai:usage:agent-graph` usage event.
   - Evaluate the graph flag via `JSONVariation`. Then validate:
     1. graph is fetchable (variation is a valid graph object, enabled),
     2. exactly one root node exists,
     3. no unconnected nodes,
     4. all child `AIAgentConfig`s are fetchable/enabled.
   - On any failure → return a definition with `Enabled() == false` and an empty
     node map. Emit DEBUG-level warnings with actionable detail
     (`Unconnected node ID <key>`, `<key> agent unable to be fetched`, etc.)
     using the SDK loggers (AIGRAPH 1.5.2).
5. **Handoff data** is opaque (`map[string]ldvalue.Value`); expose it via the
   edge but do not interpret it.

## Testing

- Wire round-trip of the AIGRAPH §1.1 example.
- Node: `IsTerminal` correctness; `Edges`/`Config` accessors.
- Definition: `GetChildNodes`/`GetParentNodes`/`TerminalNodes`/`RootNode`/
  `GetNode`; `Traverse` visits in BFS depth order and accumulates execution
  context; `ReverseTraverse` visits terminal→root honoring longest path; cycle
  safety.
- `AgentGraph` validation matrix: valid graph → enabled with full node map;
  each failure mode (unfetchable graph, disabled flag, no/multiple roots,
  unconnected node, unfetchable child config) → disabled + empty nodes + a
  DEBUG warning.
- Node trackers carry `graphKey` (integrates task 02/03).
- `$ld:ai:usage:agent-graph` emitted once.

## Acceptance criteria

- Full agent-graph model + traversal + validated `AgentGraph` method per
  AIGRAPH, returning disabled on any validation failure.
- Child node configs fetched with `graphKey` so their events attribute to the
  graph.
- `AgentGraphDefinition.CreateTracker()` returns an `AIGraphTracker` (task 07).
- `go build`, `go test -race`, `golangci-lint run` pass.
