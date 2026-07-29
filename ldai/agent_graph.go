package ldai

import (
	"sort"

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
// collections are empty and traversal methods are no-ops. CreateTracker remains available
// on disabled graphs so callers can still record an invocation failure.
type AgentGraphDefinition struct {
	enabled        bool
	flagValue      graphFlagValue
	nodes          map[string]*AgentGraphNode
	nodeKeys       []string // encounter order: root first, then BFS via edges
	graphKey       string
	variationKey   string
	version        int
	trackerFactory func() *GraphTracker
}

// Enabled reports whether the graph passed validation and all node configs were fetched.
func (d *AgentGraphDefinition) Enabled() bool { return d.enabled }

// CreateTracker returns a new GraphTracker for a fresh graph invocation.
// Returns nil if no tracker factory was wired (for example, a blank graph key).
func (d *AgentGraphDefinition) CreateTracker() *GraphTracker {
	if d == nil || d.trackerFactory == nil {
		return nil
	}
	return d.trackerFactory()
}

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

// GetParentNodes returns nodes with an outgoing edge to nodeKey, in graph encounter order.
func (d *AgentGraphDefinition) GetParentNodes(nodeKey string) []*AgentGraphNode {
	if d == nil {
		return nil
	}
	var parents []*AgentGraphNode
	for _, key := range d.nodeKeys {
		node := d.nodes[key]
		if node == nil {
			continue
		}
		for _, edge := range node.edges {
			if edge.key == nodeKey {
				parents = append(parents, node)
				break
			}
		}
	}
	return parents
}

// TerminalNodes returns nodes with no outgoing edges, in graph encounter order.
func (d *AgentGraphDefinition) TerminalNodes() []*AgentGraphNode {
	if d == nil {
		return nil
	}
	var terminals []*AgentGraphNode
	for _, key := range d.nodeKeys {
		node := d.nodes[key]
		if node != nil && node.IsTerminal() {
			terminals = append(terminals, node)
		}
	}
	return terminals
}

// TraverseFunc visits a node during graph traversal. The return value is available under
// the node's key in the scoped context of dependency-successor nodes (never written into
// the caller's initialContext map).
type TraverseFunc func(node *AgentGraphNode, context map[string]interface{}) interface{}

// Traverse visits reachable nodes in dependency (topological) order starting from the root.
// Each node is visited at most once. On cycles, the unvisited node with the lowest remaining
// in-degree is chosen next, with ties broken by discovery order (nodeKeys). Each callback
// receives a fresh context containing initialContext plus only that node's dependency
// results. initialContext is never mutated. No-op when the graph has no root or fn is nil.
func (d *AgentGraphDefinition) Traverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	order := d.nodeKeys
	indeg := make(map[string]int, len(order))
	for _, key := range order {
		indeg[key] = 0
	}
	for _, key := range order {
		for _, child := range d.GetChildNodes(key) {
			indeg[child.key]++
		}
	}
	indeg[root.key] = 0

	visited := make(map[string]struct{}, len(order))
	results := make(map[string]interface{}, len(order))
	ancestors := make(map[string]map[string]struct{}, len(order))

	for len(visited) < len(order) {
		next := ""
		for _, k := range order {
			if _, seen := visited[k]; seen {
				continue
			}
			if indeg[k] == 0 {
				next = k
				break
			}
		}
		if next == "" {
			// Cycle: pick unvisited with lowest in-degree; discovery order breaks ties.
			lowest := -1
			for _, k := range order {
				if _, seen := visited[k]; seen {
					continue
				}
				if lowest < 0 || indeg[k] < lowest {
					lowest = indeg[k]
					next = k
				}
			}
		}
		if next == "" {
			return
		}

		visited[next] = struct{}{}
		anc := make(map[string]struct{})
		for _, parent := range d.GetParentNodes(next) {
			if parent.key == next {
				continue
			}
			if _, seen := visited[parent.key]; !seen {
				continue
			}
			anc[parent.key] = struct{}{}
			for k := range ancestors[parent.key] {
				anc[k] = struct{}{}
			}
		}
		ancestors[next] = anc

		node := d.nodes[next]
		results[next] = fn(node, freshContext(initialContext, results, anc))

		for _, child := range d.GetChildNodes(next) {
			indeg[child.key]--
		}
	}
}

// ReverseTraverse visits reachable nodes in reverse dependency order (root last).
// Non-root nodes are released by out-degree (Kahn); on cycles among non-root nodes the
// unvisited non-root with the lowest remaining out-degree is chosen next, with ties broken
// by discovery order. Each callback receives a fresh context containing initialContext plus
// only that node's descendant results. initialContext is never mutated. No-op when the
// graph has no root or fn is nil. Pure cycles still visit every node (root last).
func (d *AgentGraphDefinition) ReverseTraverse(fn TraverseFunc, initialContext map[string]interface{}) {
	root := d.RootNode()
	if root == nil || fn == nil {
		return
	}

	order := d.nodeKeys
	outdeg := make(map[string]int, len(order))
	for _, key := range order {
		outdeg[key] = len(d.GetChildNodes(key))
	}

	visited := make(map[string]struct{}, len(order))
	results := make(map[string]interface{}, len(order))
	descendants := make(map[string]map[string]struct{}, len(order))

	nonRootCount := 0
	for _, k := range order {
		if k != root.key {
			nonRootCount++
		}
	}

	for len(visited) < nonRootCount {
		next := ""
		for _, k := range order {
			if k == root.key {
				continue
			}
			if _, seen := visited[k]; seen {
				continue
			}
			if outdeg[k] == 0 {
				next = k
				break
			}
		}
		if next == "" {
			// Cycle among non-root nodes: lowest out-degree; discovery order breaks ties.
			lowest := -1
			for _, k := range order {
				if k == root.key {
					continue
				}
				if _, seen := visited[k]; seen {
					continue
				}
				if lowest < 0 || outdeg[k] < lowest {
					lowest = outdeg[k]
					next = k
				}
			}
		}
		if next == "" {
			break
		}

		visited[next] = struct{}{}
		desc := make(map[string]struct{})
		for _, child := range d.GetChildNodes(next) {
			if child.key == next {
				continue
			}
			if _, seen := visited[child.key]; !seen {
				continue
			}
			desc[child.key] = struct{}{}
			for k := range descendants[child.key] {
				desc[k] = struct{}{}
			}
		}
		descendants[next] = desc

		node := d.nodes[next]
		results[next] = fn(node, freshContext(initialContext, results, desc))

		for _, parent := range d.GetParentNodes(next) {
			if parent.key == root.key {
				continue
			}
			outdeg[parent.key]--
		}
	}

	if _, seen := visited[root.key]; !seen {
		visited[root.key] = struct{}{}
		allNonRoot := make(map[string]struct{}, nonRootCount)
		for _, k := range order {
			if k != root.key {
				allNonRoot[k] = struct{}{}
			}
		}
		descendants[root.key] = allNonRoot
		results[root.key] = fn(root, freshContext(initialContext, results, allNonRoot))
	}
}

// freshContext returns a new map with initialContext entries plus results for keys in deps.
// The caller's initial map is never mutated.
func freshContext(
	initial map[string]interface{},
	results map[string]interface{},
	deps map[string]struct{},
) map[string]interface{} {
	ctx := make(map[string]interface{}, len(initial)+len(deps))
	for k, v := range initial {
		ctx[k] = v
	}
	for k := range deps {
		if v, ok := results[k]; ok {
			ctx[k] = v
		}
	}
	return ctx
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

// orderedNodeKeys returns keys in encounter (discovery) order: root first, then BFS via
// declared edge order. Unreachable leftovers from allKeys are appended in sorted order so
// discovery/tie-break never depends on Go map iteration.
func orderedNodeKeys(flagValue graphFlagValue, allKeys map[string]struct{}) []string {
	ordered := make([]string, 0, len(allKeys))
	seen := make(map[string]struct{}, len(allKeys))

	appendKey := func(key string) {
		if key == "" {
			return
		}
		if _, ok := allKeys[key]; !ok {
			return
		}
		if _, already := seen[key]; already {
			return
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}

	if flagValue.root != "" {
		appendKey(flagValue.root)
		queue := []string{flagValue.root}
		for len(queue) > 0 {
			key := queue[0]
			queue = queue[1:]
			for _, edge := range flagValue.edges[key] {
				if edge.key == "" {
					continue
				}
				if _, already := seen[edge.key]; already {
					continue
				}
				if _, ok := allKeys[edge.key]; !ok {
					continue
				}
				appendKey(edge.key)
				queue = append(queue, edge.key)
			}
		}
	}

	if len(seen) < len(allKeys) {
		leftovers := make([]string, 0, len(allKeys)-len(seen))
		for key := range allKeys {
			if _, already := seen[key]; !already {
				leftovers = append(leftovers, key)
			}
		}
		sort.Strings(leftovers)
		for _, key := range leftovers {
			appendKey(key)
		}
	}
	return ordered
}

func buildGraphNodes(
	flagValue graphFlagValue,
	configs map[string]AIAgentConfig,
) (map[string]*AgentGraphNode, []string) {
	allKeys := collectAllKeys(flagValue)
	nodeKeys := orderedNodeKeys(flagValue, allKeys)
	nodes := make(map[string]*AgentGraphNode, len(allKeys))
	for _, key := range nodeKeys {
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
	// Keep nodeKeys only for nodes that were actually built.
	if len(nodes) != len(nodeKeys) {
		filtered := make([]string, 0, len(nodes))
		for _, key := range nodeKeys {
			if _, ok := nodes[key]; ok {
				filtered = append(filtered, key)
			}
		}
		nodeKeys = filtered
	}
	return nodes, nodeKeys
}

func newDisabledAgentGraphDefinition(flagValue graphFlagValue, graphKey string) AgentGraphDefinition {
	return AgentGraphDefinition{
		enabled:      false,
		flagValue:    flagValue,
		nodes:        map[string]*AgentGraphNode{},
		nodeKeys:     nil,
		graphKey:     graphKey,
		variationKey: flagValue.variationKey,
		version:      flagValue.version,
	}
}
