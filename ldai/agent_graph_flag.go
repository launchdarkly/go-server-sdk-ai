package ldai

import (
	"maps"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
)

const defaultGraphVersion = 1

// graphFlagValue is the parsed wire representation of an agent graph flag variation.
type graphFlagValue struct {
	root         string
	edges        map[string][]GraphEdge
	variationKey string
	version      int
	enabled      bool
}

func disabledGraphFlagValue() graphFlagValue {
	return graphFlagValue{
		root:    "",
		edges:   nil,
		version: defaultGraphVersion,
		enabled: false,
	}
}

// parseGraphFlagValue defensively parses a JSON variation into a graphFlagValue.
// Non-object values yield a disabled flag value. Missing _ldMeta.enabled defaults to true;
// missing _ldMeta.version defaults to 1. Malformed edges are skipped.
func parseGraphFlagValue(value ldvalue.Value) graphFlagValue {
	if value.Type() != ldvalue.ObjectType {
		return disabledGraphFlagValue()
	}

	parsed := graphFlagValue{
		version: defaultGraphVersion,
		enabled: true,
	}

	meta := value.GetByKey("_ldMeta")
	if meta.Type() == ldvalue.ObjectType {
		if enabledVal := meta.GetByKey("enabled"); enabledVal.Type() == ldvalue.BoolType {
			parsed.enabled = enabledVal.BoolValue()
		}
		if vk := meta.GetByKey("variationKey"); vk.Type() == ldvalue.StringType {
			parsed.variationKey = vk.StringValue()
		}
		if ver := meta.GetByKey("version"); ver.Type() == ldvalue.NumberType {
			parsed.version = ver.IntValue()
		}
	}

	if rootVal := value.GetByKey("root"); rootVal.Type() == ldvalue.StringType {
		parsed.root = rootVal.StringValue()
	}

	edgesVal := value.GetByKey("edges")
	if edgesVal.Type() == ldvalue.ObjectType {
		edges := make(map[string][]GraphEdge, edgesVal.Count())
		for _, sourceKey := range edgesVal.Keys(nil) {
			edgeArray := edgesVal.GetByKey(sourceKey)
			if edgeArray.Type() != ldvalue.ArrayType {
				continue
			}
			edgeList := make([]GraphEdge, 0, edgeArray.Count())
			for i := range edgeArray.Count() {
				edgeObj := edgeArray.GetByIndex(i)
				if edgeObj.Type() != ldvalue.ObjectType {
					continue
				}
				keyVal := edgeObj.GetByKey("key")
				if keyVal.Type() != ldvalue.StringType {
					continue
				}
				targetKey := keyVal.StringValue()
				if targetKey == "" {
					continue
				}
				edgeList = append(edgeList, GraphEdge{
					key:     targetKey,
					handoff: parseHandoff(edgeObj.GetByKey("handoff")),
				})
			}
			edges[sourceKey] = edgeList
		}
		parsed.edges = edges
	}

	return parsed
}

func parseHandoff(handoff ldvalue.Value) map[string]ldvalue.Value {
	if handoff.Type() != ldvalue.ObjectType {
		return nil
	}
	if handoff.Count() == 0 {
		return nil
	}
	result := make(map[string]ldvalue.Value, handoff.Count())
	for _, k := range handoff.Keys(nil) {
		result[k] = handoff.GetByKey(k)
	}
	return result
}

func cloneHandoff(handoff map[string]ldvalue.Value) map[string]ldvalue.Value {
	if handoff == nil {
		return nil
	}
	return maps.Clone(handoff)
}
