package backendmodel

import (
	"context"
	"maps"
	"slices"
)

// Projection changes may cross a different provider claim on the same UUID.
// Extend the counted read closure after selection without widening writes.
func (p *composedGraphPreparation) includeIncrementalProjectionDependencies(ctx context.Context, changed map[string]bool) error {
	scope := p.candidate.IncrementalScope
	if scope == nil || len(changed) == 0 {
		return nil
	}
	c := &incrementalClosure{
		readOnly: true, ctx: ctx,
		input:  incrementalScopeInput{base: p.candidate.Source, session: p.session},
		claims: map[string]ProviderAssertion{}, subjects: map[SourceSubjectRef][]string{}, selectedKeys: map[string]string{},
		outgoing: map[string][]incrementalArc{}, incoming: map[string][]incrementalArc{}, incomingContext: map[incrementalVisit][]incrementalArc{},
		seen: maps.Clone(scope.contexts), affected: map[SourceSubjectRef]bool{}, validation: map[SourceSubjectRef]bool{}, foreign: map[SourceSubjectRef]bool{}, result: scope,
	}
	if err := c.index(); err != nil {
		return err
	}
	for _, ref := range scope.Affected {
		c.affected[ref] = true
	}
	for _, ref := range scope.ValidationDependencies {
		c.validation[ref] = true
	}
	for _, ref := range scope.ForeignDependencies {
		c.foreign[ref] = true
	}
	// Existing visits stay charged but must not suppress expansion from a new
	// projection boundary whose relevant reverse arc was absent from base scope.
	for _, key := range slices.Sorted(maps.Keys(changed)) {
		visit := incrementalVisit{claim: key}
		if c.seen[visit] {
			c.queue = append(c.queue, visit)
			continue
		}
		if err := c.push(visit); err != nil {
			return err
		}
	}
	if err := c.walk(); err != nil {
		return err
	}
	for ref := range c.affected {
		delete(c.validation, ref)
	}
	scope.ValidationDependencies = sortedIncrementalSubjects(c.validation)
	scope.ForeignDependencies = sortedIncrementalSubjects(c.foreign)
	scope.contexts, scope.visitedCount = c.seen, len(c.seen)
	scope.UntouchedCount = 0
	for key, a := range c.claims {
		if c.selected(a) && !scope.claims[key] {
			scope.UntouchedCount++
		}
	}
	return nil
}
