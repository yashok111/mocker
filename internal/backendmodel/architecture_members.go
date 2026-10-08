package backendmodel

import (
	"cmp"
	"slices"
)

type architectureProjection struct {
	targetHash   string
	identityKeys map[string]string
	identityErr  error
	memberSeen   map[string]map[string]bool
	elements     map[string]ArchitectureElement
	links        map[string]ArchitectureLink
	members      map[string][]DiagramMember
	gaps         []DiagramGap
	truncated    bool
}

func (p *architectureProjection) addMember(id string, m DiagramMember) {
	hash, _ := requestDigest(m.Ref)
	if p.memberSeen == nil {
		p.memberSeen = map[string]map[string]bool{}
	}
	if p.memberSeen[id] == nil {
		p.memberSeen[id] = map[string]bool{}
	}
	if p.memberSeen[id][hash] {
		return
	}
	p.memberSeen[id][hash] = true
	p.members[id] = append(p.members[id], m)
}
func (p *architectureProjection) sortedMembers(id string) []DiagramMember {
	out := slices.Clone(p.members[id])
	if out == nil {
		out = []DiagramMember{}
	}
	slices.SortFunc(out, func(a, b DiagramMember) int {
		key := func(r DiagramRef) string {
			if r.Kind == "record" {
				return r.RecordType + ":" + r.ID
			}
			hash, _ := requestDigest(r)
			return "artifact:" + hash
		}
		return cmp.Compare(key(a.Ref), key(b.Ref))
	})
	return out
}
