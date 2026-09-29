package apidesign

import (
	"context"
	"slices"
)

const (
	impactMaxChanges    = 500
	impactMaxReferences = 10000
	impactMaxVisits     = 200000
	impactMaxEntities   = 5000
	impactMaxEvidence   = 10000
	impactMaxDepth      = 128
	impactMaxExcerpt    = 4096
)

type impactCollector struct {
	ctx         context.Context
	result      ImpactAnalysis
	visits      int
	entities    map[string]int
	evidence    map[string]int
	diagnostics map[string]bool
}

func newImpactCollector(ctx context.Context) *impactCollector {
	return &impactCollector{ctx: ctx, result: ImpactAnalysis{
		Changes: []ImpactChange{}, Affected: []ImpactEntity{}, Evidence: []ImpactEvidence{}, Diagnostics: []ImpactDiagnostic{},
		Coverage: ImpactCoverage{TruncatedReasons: []string{}}, Complete: true,
	}, entities: map[string]int{}, evidence: map[string]int{}, diagnostics: map[string]bool{}}
}
func (c *impactCollector) truncate(reason string) {
	c.result.Complete = false
	if !slices.Contains(c.result.Coverage.TruncatedReasons, reason) {
		c.result.Coverage.TruncatedReasons = append(c.result.Coverage.TruncatedReasons, reason)
	}
}
func (c *impactCollector) visit() bool {
	if c.ctx.Err() != nil {
		return false
	}
	c.visits++
	if c.visits > impactMaxVisits {
		c.truncate("traversal")
		return false
	}
	return true
}
func (c *impactCollector) diagnostic(d ImpactDiagnostic, incomplete bool) {
	if incomplete {
		c.result.Complete = false
	}
	key := d.Code + "\x00" + d.Side + "\x00" + d.Pointer
	if c.diagnostics[key] {
		return
	}
	c.diagnostics[key] = true
	// Reserve a bounded diagnostic surface even for documents full of bad refs.
	if len(c.result.Diagnostics) >= 500 {
		c.truncate("traversal")
		return
	}
	c.result.Diagnostics = append(c.result.Diagnostics, d)
}
func (c *impactCollector) affect(entity ImpactEntity, evidence ImpactEvidence) {
	key := evidence.ChangeID + "\x00" + entity.ID + "\x00" + evidence.Side + "\x00" + evidence.Direction
	if index, exists := c.evidence[key]; exists {
		old := c.result.Evidence[index]
		if len(old.ReferenceSites) <= len(evidence.ReferenceSites) {
			return
		}
		evidence.ID = old.ID
		evidence.EntityID = entity.ID
		if evidence.ReferenceSites == nil {
			evidence.ReferenceSites = []ImpactReferenceSite{}
		}
		c.result.Evidence[index] = evidence
		return
	}
	if len(c.result.Evidence) >= impactMaxEvidence {
		c.truncate("evidence")
		return
	}
	index, exists := c.entities[entity.ID]
	if !exists {
		if len(c.result.Affected) >= impactMaxEntities {
			c.truncate("entities")
			return
		}
		index = len(c.result.Affected)
		c.entities[entity.ID] = index
		c.result.Affected = append(c.result.Affected, entity)
	} else {
		if entity.Before != nil {
			c.result.Affected[index].Before = entity.Before
		}
		if entity.After != nil {
			c.result.Affected[index].After = entity.After
		}
	}
	c.evidence[key] = len(c.result.Evidence)
	evidence.ID = impactID("evidence", key)
	evidence.EntityID = entity.ID
	if evidence.ReferenceSites == nil {
		evidence.ReferenceSites = []ImpactReferenceSite{}
	}
	c.result.Evidence = append(c.result.Evidence, evidence)
}
