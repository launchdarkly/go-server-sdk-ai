package ldai

import (
	"sort"

	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
)

// maxTraversalDepth bounds the number of level-walk iterations during depth assignment.
const maxTraversalDepth = 100

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
func (n *AgentGraphNode) Config() *AIAgentConfig { return &n.config }

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

// GetParentNodes returns nodes with an outgoing edge to nodeKey, sorted by key.
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
	sort.Slice(parents, func(i, j int) bool { return parents[i].key < parents[j].key })
	return parents
}

// TerminalNodes returns nodes with no outgoing edges, sorted by key.
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
	sort.Slice(terminals, func(i, j int) bool { return terminals[i].key < terminals[j].key })
	return terminals
}

// TraverseFunc visits a node during graph traversal. The return value is stored in the
// context map under the node's key for use by subsequently visited nodes.
type TraverseFunc func(node *AgentGraphNode, context map[string]interface{}) interface{}

// Traverse visits nodes in longest-path depth order from the root (shallow before deep).
// Within a depth, nodes are visited in sorted key order. Each node is visited at most once.
// No-op when the graph has no root or fn is nil.
func (d *AgentGraphDefinition) Traverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	ctx := initialContext
	if ctx == nil {
		ctx = make(map[string]interface{})
	}

	d.executeByDepth(fn, ctx, true)
}

// ReverseTraverse visits nodes deepest-first (root last), using longest-path depth order.
// Within a depth, nodes are visited in sorted key order. No-op when there is no root, fn is
// nil, or there are no terminals (e.g. a pure cycle).
func (d *AgentGraphDefinition) ReverseTraverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	terminals := d.TerminalNodes()
	if len(terminals) == 0 {
		return
	}

	ctx := initialContext
	if ctx == nil {
		ctx = make(map[string]interface{})
	}

	d.executeByDepth(fn, ctx, false)
}

// assignLongestPathDepths returns longest-path depth from the root for each reachable node.
func (d *AgentGraphDefinition) assignLongestPathDepths() map[string]int {
	root := d.RootNode()
	if root == nil {
		return nil
	}

	depths := map[string]int{root.key: 0}
	seen := map[string]struct{}{root.key: struct{}{}}
	frontier := []string{root.key}
	maxDepth := 0

	for iterations := 0; len(frontier) > 0 && iterations < maxTraversalDepth; iterations++ {
		next := make([]string, 0)
		for _, nodeKey := range frontier {
			depth := depths[nodeKey]
			for _, child := range d.GetChildNodes(nodeKey) {
				childDepth := depth + 1
				existing, ok := depths[child.key]
				if ok && childDepth > existing && existing < depth {
					continue // cycle back-edge
				}
				if !ok || childDepth > existing {
					depths[child.key] = childDepth
					if childDepth > maxDepth {
						maxDepth = childDepth
					}
				}
				if _, already := seen[child.key]; !already {
					seen[child.key] = struct{}{}
					next = append(next, child.key)
				}
			}
		}
		frontier = next
	}

	for key := range seen {
		if _, ok := depths[key]; !ok {
			depths[key] = maxDepth
		}
	}
	return depths
}

// executeByDepth invokes fn by depth (ascending or descending), sorting keys within each depth.
func (d *AgentGraphDefinition) executeByDepth(fn TraverseFunc, ctx map[string]interface{}, ascending bool) {
	depths := d.assignLongestPathDepths()
	if len(depths) == 0 {
		return
	}

	byDepth := make(map[int][]string)
	for key, depth := range depths {
		byDepth[depth] = append(byDepth[depth], key)
	}

	levels := make([]int, 0, len(byDepth))
	for depth := range byDepth {
		levels = append(levels, depth)
	}
	if ascending {
		sort.Ints(levels)
	} else {
		sort.Sort(sort.Reverse(sort.IntSlice(levels)))
	}

	for _, depth := range levels {
		keys := byDepth[depth]
		sort.Strings(keys)
		for _, key := range keys {
			node := d.nodes[key]
			if node == nil {
				continue
			}
			ctx[key] = fn(node, ctx)
		}
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
