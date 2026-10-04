package backendmodel

import "slices"

func incrementalRetainedCurrentness(
	s *ImportSession,
	base *SourceGraphSnapshot,
	a ProviderAssertion,
	prior SourceClaimCurrentness,
	scope *IncrementalAffectedScope,
) (SourceClaimCurrentness, error) {
	selected := a.Owner.RepositoryID == s.RepositoryID && a.Owner.ProviderNamespace == s.Manifest.Provider.Namespace
	if !selected {
		return prior, nil
	}
	// Proof admission remains exact even when currentness must become stale.
	if err := validateSourceOwnProof(s, a, base, false, nil); err != nil {
		return SourceClaimCurrentness{}, err
	}
	safe, err := incrementalUnchangedProof(s, base, a, prior, scope)
	if err != nil {
		return SourceClaimCurrentness{}, err
	}
	out := prior
	out.Fields = slices.Clone(prior.Fields)
	if !safe {
		out.Own = sourceStaleReason(out.Own, "not_reobserved")
		for i := range out.Fields {
			out.Fields[i].Own = sourceStaleReason(out.Fields[i].Own, "not_reobserved")
		}
		return out, nil
	}
	out.Own = incrementalUnchangedBasis(out.Own)
	for i := range out.Fields {
		if out.Fields[i].Own.Status == "current" {
			out.Fields[i].Own = incrementalUnchangedBasis(out.Fields[i].Own)
		}
	}
	return out, nil
}

func incrementalUnchangedProof(
	s *ImportSession,
	base *SourceGraphSnapshot,
	a ProviderAssertion,
	prior SourceClaimCurrentness,
	scope *IncrementalAffectedScope,
) (bool, error) {
	previouslyCurrent := prior.Own.Status == "current" && prior.Dependency.Status == "current"
	verified := s.Manifest.Snapshot.Consistency == "verified"
	if !previouslyCurrent || !verified || scope == nil || scope.affectsOwn(sourceAssertionKey(a)) {
		return false, nil
	}
	index, err := sourceProofs(base)
	if err != nil {
		return false, err
	}
	if len(a.EvidenceIDs) == 0 || sourceMetadataOnly(a, index) {
		return false, nil
	}
	old, next := scope.beforeFiles, scope.afterFiles
	if old == nil || next == nil {
		before, err := incrementalBaseSnapshot(base, s)
		if err != nil {
			return false, err
		}
		old, err := incrementalManifestFiles(before.Files)
		if err != nil {
			return false, err
		}
		next, err := incrementalManifestFiles(s.Manifest.Snapshot.Files)
		if err != nil {
			return false, err
		}
		scope.beforeFiles, scope.afterFiles = old, next
	}
	old, next = scope.beforeFiles, scope.afterFiles
	if !incrementalSupportingFilesUnchanged(a, index, old, next) {
		return false, nil
	}
	for _, binding := range a.DependencyClaims {
		if scope.affectsDependency(binding) {
			return false, nil
		}
	}
	return true, nil
}

func incrementalSupportingFilesUnchanged(a ProviderAssertion, index *sourceProofIndex, old, next map[string]ManifestFile) bool {
	for _, id := range a.EvidenceIDs {
		e := index.evidence[id]
		priorFile, wasAvailable := old[e.Source.File]
		nextFile, available := next[e.Source.File]
		matchingHash := priorFile.ContentHash == e.Source.ContentHash && nextFile.ContentHash == e.Source.ContentHash
		analyzed := priorFile.AnalysisStatus == "analyzed" && nextFile.AnalysisStatus == "analyzed"
		if !wasAvailable || !available || !matchingHash || !analyzed || priorFile.FileType != nextFile.FileType {
			return false
		}
	}
	return true
}

func incrementalUnchangedBasis(f AssertionFreshness) AssertionFreshness {
	f.Reasons = append(slices.Clone(f.Reasons), "unchanged_source_manifest")
	slices.Sort(f.Reasons)
	f.Reasons = slices.Compact(f.Reasons)
	return f
}

func (p *composedGraphPreparation) applyIncrementalDependencyGaps() {
	scope := p.candidate.IncrementalScope
	if scope == nil {
		return
	}
	for key := range scope.dependentClaims {
		a, ok := p.claims[key]
		if !ok || p.selected(a) && scope.writable[key] {
			continue
		}
		// A selected caller reached only for validation may become dependency
		// stale without being authorized for reobservation or changing own proof.
		current := p.currentness[key]
		current.Dependency = sourceStaleReason(current.Dependency, "dependency_changed")
		p.currentness[key] = current
	}
}

func (p *composedGraphPreparation) finalizeIncrementalGaps() {
	scope := p.candidate.IncrementalScope
	if scope == nil {
		return
	}
	for _, current := range p.candidate.Source.Currentness {
		if current.Own.Status == "current" && current.Dependency.Status == "current" {
			continue
		}
		selected := current.RepositoryID == p.session.RepositoryID && current.ProviderNamespace == p.session.Manifest.Provider.Namespace
		reason := "selected_proof_stale"
		if !selected {
			if current.Dependency.Status == "current" {
				continue
			}
			reason = "foreign_dependency_stale"
		}
		scope.Gaps = append(scope.Gaps, reason+": "+current.RepositoryID+"/"+current.ProviderNamespace+"/"+current.RecordType+"/"+current.RecordID)
	}
	slices.Sort(scope.Gaps)
	scope.Gaps = slices.Compact(scope.Gaps)
}

func (scope *IncrementalAffectedScope) affectsOwn(key string) bool {
	if scope.wholeClaims == nil {
		return scope.claims[key]
	}
	return scope.wholeClaims[key]
}

func (scope *IncrementalAffectedScope) affectsDependency(binding SourceDependencyBinding) bool {
	target := binding.Target
	key := sourceClaimKey(target.RecordType, target.ExpectedID, target.RepositoryID, target.ProviderNamespace)
	if scope.affectsOwn(key) {
		return true
	}
	if binding.ValueContext == nil {
		return scope.claims[key]
	}
	return scope.contexts[incrementalVisit{claim: key, value: *binding.ValueContext, contextual: true}]
}

func incrementalRevisionBytes(s *ImportSession, c *composedCandidate) (int, error) {
	if c.IncrementalScope == nil {
		return 0, nil
	}
	raw, err := source6RevisionDecisions(s, c)
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}
