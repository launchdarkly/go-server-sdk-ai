package ldai

import (
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
)

// GraphEdge is a directed edge from a source node to a target node in an agent graph.
// The source is implicit — it is the node that owns this edge.
type GraphEdge struct {
	key     string
	handoff map[string]ldvalue.Value
}

// Key returns the target node config key.
func (e GraphEdge) Key() string { return e.key }

// Handoff returns a defensive copy of optional handoff data associated with this edge, or nil.
func (e GraphEdge) Handoff() map[string]ldvalue.Value { return cloneHandoff(e.handoff) }

// AgentGraphNode is a single node in a resolved agent graph.
type AgentGraphNode struct {
	key    string
	config AIAgentConfig
	edges  []GraphEdge
}

// Key returns the node config key.
func (n *AgentGraphNode) Key() string { return n.key }

// Config returns the agent config for this node.
func (n *AgentGraphNode) Config() *AIAgentConfig {
	if n == nil {
		return nil
	}
	return &n.config
}

// Edges returns a copy of the outgoing edges from this node.
func (n *AgentGraphNode) Edges() []GraphEdge {
	if len(n.edges) == 0 {
		return nil
	}
	out := make([]GraphEdge, len(n.edges))
	copy(out, n.edges)
	return out
}

// IsTerminal reports whether this node has no outgoing edges.
func (n *AgentGraphNode) IsTerminal() bool { return len(n.edges) == 0 }

// AgentGraphDefinition is a fully resolved agent graph returned by Client.AgentGraph.
//
// When Enabled returns false, the graph was not fetchable or failed validation; node
// collections are empty and traversal methods are no-ops.
type AgentGraphDefinition struct {
	enabled      bool
	flagValue    graphFlagValue
	nodes        map[string]*AgentGraphNode
	graphKey     string
	variationKey string
	version      int
}

// Enabled reports whether the graph passed validation and all node configs were fetched.
func (d *AgentGraphDefinition) Enabled() bool { return d.enabled }

// RootNode returns the root node, or nil if the graph is disabled or the root is absent.
func (d *AgentGraphDefinition) RootNode() *AgentGraphNode {
	if d == nil || d.flagValue.root == "" {
		return nil
	}
	return d.nodes[d.flagValue.root]
}

// GetNode returns the node with the given key, or nil if not found.
func (d *AgentGraphDefinition) GetNode(nodeKey string) *AgentGraphNode {
	if d == nil {
		return nil
	}
	return d.nodes[nodeKey]
}

// GetChildNodes returns the immediate children of the node with the given key by following
// its outgoing edges. Missing targets are skipped. Returns nil if the node is unknown.
func (d *AgentGraphDefinition) GetChildNodes(nodeKey string) []*AgentGraphNode {
	node := d.GetNode(nodeKey)
	if node == nil {
		return nil
	}
	children := make([]*AgentGraphNode, 0, len(node.edges))
	for _, edge := range node.edges {
		if child := d.nodes[edge.key]; child != nil {
			children = append(children, child)
		}
	}
	return children
}

// GetParentNodes returns all nodes that have an outgoing edge pointing to nodeKey.
func (d *AgentGraphDefinition) GetParentNodes(nodeKey string) []*AgentGraphNode {
	if d == nil {
		return nil
	}
	var parents []*AgentGraphNode
	for _, node := range d.nodes {
		for _, edge := range node.edges {
			if edge.key == nodeKey {
				parents = append(parents, node)
				break
			}
		}
	}
	return parents
}

// TerminalNodes returns all nodes with no outgoing edges.
func (d *AgentGraphDefinition) TerminalNodes() []*AgentGraphNode {
	if d == nil {
		return nil
	}
	var terminals []*AgentGraphNode
	for _, node := range d.nodes {
		if node.IsTerminal() {
			terminals = append(terminals, node)
		}
	}
	return terminals
}

// TraverseFunc visits a node during graph traversal. The return value is stored in the
// context map under the node's key for use by subsequently visited nodes.
type TraverseFunc func(node *AgentGraphNode, context map[string]interface{}) interface{}

// Traverse performs a breadth-first traversal starting from the root. Each node is visited
// at most once (cycle-safe). This is a no-op when the graph is disabled or has no root.
//
// If initialContext is nil, a new map is created. The visitor's return value is stored under
// the node key in the context map.
func (d *AgentGraphDefinition) Traverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	ctx := initialContext
	if ctx == nil {
		ctx = make(map[string]interface{})
	}

	visited := make(map[string]struct{})
	queue := []*AgentGraphNode{root}
	visited[root.key] = struct{}{}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		result := fn(node, ctx)
		ctx[node.key] = result

		for _, child := range d.GetChildNodes(node.key) {
			if _, seen := visited[child.key]; !seen {
				visited[child.key] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
}

// ReverseTraverse performs a reverse breadth-first traversal starting from terminal nodes
// and working toward the root. The root node is always processed last. Each node is visited
// at most once (cycle-safe). This is a no-op when the graph is disabled or has no root.
func (d *AgentGraphDefinition) ReverseTraverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	ctx := initialContext
	if ctx == nil {
		ctx = make(map[string]interface{})
	}

	visited := make(map[string]struct{})
	var queue []*AgentGraphNode

	// Seed from terminals, excluding root (it is processed last).
	for _, terminal := range d.TerminalNodes() {
		if terminal.key == root.key {
			continue
		}
		if _, seen := visited[terminal.key]; !seen {
			visited[terminal.key] = struct{}{}
			queue = append(queue, terminal)
		}
	}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		result := fn(node, ctx)
		ctx[node.key] = result

		for _, parent := range d.GetParentNodes(node.key) {
			if parent.key == root.key {
				continue
			}
			if _, seen := visited[parent.key]; !seen {
				visited[parent.key] = struct{}{}
				queue = append(queue, parent)
			}
		}
	}

	// Process root last.
	if _, seen := visited[root.key]; !seen {
		result := fn(root, ctx)
		ctx[root.key] = result
	}
}

func collectAllKeys(flagValue graphFlagValue) map[string]struct{} {
	keys := make(map[string]struct{})
	if flagValue.root != "" {
		keys[flagValue.root] = struct{}{}
	}
	for source, edges := range flagValue.edges {
		keys[source] = struct{}{}
		for _, edge := range edges {
			if edge.key != "" {
				keys[edge.key] = struct{}{}
			}
		}
	}
	return keys
}

func collectReachableKeys(flagValue graphFlagValue) map[string]struct{} {
	visited := make(map[string]struct{})
	if flagValue.root == "" {
		return visited
	}
	queue := []string{flagValue.root}
	visited[flagValue.root] = struct{}{}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, edge := range flagValue.edges[key] {
			if _, seen := visited[edge.key]; !seen && edge.key != "" {
				visited[edge.key] = struct{}{}
				queue = append(queue, edge.key)
			}
		}
	}
	return visited
}

func buildGraphNodes(flagValue graphFlagValue, configs map[string]AIAgentConfig) map[string]*AgentGraphNode {
	allKeys := collectAllKeys(flagValue)
	nodes := make(map[string]*AgentGraphNode, len(allKeys))
	for key := range allKeys {
		config, ok := configs[key]
		if !ok {
			continue
		}
		edges := flagValue.edges[key]
		if edges == nil {
			edges = []GraphEdge{}
		} else {
			// Defensive copy so nodes own their edge slices.
			edges = append([]GraphEdge(nil), edges...)
			for i := range edges {
				edges[i].handoff = cloneHandoff(edges[i].handoff)
			}
		}
		nodes[key] = &AgentGraphNode{
			key:    key,
			config: config,
			edges:  edges,
		}
	}
	return nodes
}

func newDisabledAgentGraphDefinition(flagValue graphFlagValue, graphKey string) AgentGraphDefinition {
	return AgentGraphDefinition{
		enabled:      false,
		flagValue:    flagValue,
		nodes:        map[string]*AgentGraphNode{},
		graphKey:     graphKey,
		variationKey: flagValue.variationKey,
		version:      flagValue.version,
	}
}
