package backendmodel

type effectiveGraphReadIndex struct {
	payloads map[string]SourceAssertionPayload
	origins  map[string]EffectiveFieldOrigin
}

func (graph *EffectiveGraphSnapshot) indexedReads() *effectiveGraphReadIndex {
	if graph.readIndex != nil {
		return graph.readIndex
	}
	index := &effectiveGraphReadIndex{payloads: map[string]SourceAssertionPayload{}, origins: map[string]EffectiveFieldOrigin{}}
	for _, n := range graph.State.Nodes {
		index.payloads["node\x00"+n.ID] = sourceNodePayload(n)
	}
	for _, e := range graph.State.Edges {
		index.payloads["edge\x00"+e.ID] = sourceEdgePayload(e)
	}
	for _, origin := range graph.Origins {
		if origin.Selector.Source != nil {
			index.origins[origin.RecordType+"\x00"+origin.SubjectID+"\x00"+sourcePropertyKey(*origin.Selector.Source)] = origin
		}
	}
	graph.readIndex = index
	return index
}
