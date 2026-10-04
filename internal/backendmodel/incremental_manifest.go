package backendmodel

func validateChangeManifestShape(changes ChangeManifest) error {
	if changes.Files == nil || changes.AffectedRoots == nil {
		return semantic("changeManifest", "Files and affectedRoots must be non-null arrays")
	}
	if changes.Scope != "affected-subgraph" {
		return semantic("changeManifest.scope", "Incremental imports require affected-subgraph scope")
	}
	if len(changes.Files) > MaxManifestFiles || len(changes.AffectedRoots) > MaxIncrementalSubjects {
		return limitFault("Incremental manifest exceeds limits; use whole-source-v1")
	}
	paths := map[string]bool{}
	for _, file := range changes.Files {
		if !validPath(file.Path) || paths[file.Path] {
			return semantic("changeManifest.files.path", "File paths must be normalized, unique and repository-relative")
		}
		paths[file.Path] = true
		valid := false
		switch file.Kind {
		case "added":
			valid = file.BeforeHash == "" && validHash(file.AfterHash)
		case "modified":
			valid = validHash(file.BeforeHash) && validHash(file.AfterHash) && file.BeforeHash != file.AfterHash
		case "deleted":
			valid = validHash(file.BeforeHash) && file.AfterHash == ""
		}
		if !valid {
			return semantic("changeManifest.files", "Invalid tagged file change or content hash")
		}
	}
	return validateIncrementalRoots(changes.AffectedRoots)
}

func validateIncrementalRoots(affected []SourceSubjectRef) error {
	roots := map[SourceSubjectRef]bool{}
	for _, root := range affected {
		validType := root.RecordType == "node" || root.RecordType == "edge" || root.RecordType == "evidence"
		if !validType || !ValidID(root.ID) || roots[root] {
			return semantic("changeManifest.affectedRoots", "Roots require unique exact node, edge or evidence identities")
		}
		roots[root] = true
	}
	return nil
}

func ValidateChangeManifest(before SourceSnapshot, after SnapshotManifest, changes ChangeManifest) error {
	if err := validateChangeManifestShape(changes); err != nil {
		return err
	}
	old, err := incrementalManifestFiles(before.Files)
	if err != nil {
		return err
	}
	next, err := incrementalManifestFiles(after.Files)
	if err != nil {
		return err
	}
	diff := map[string]ChangeManifestFile{}
	for path, file := range old {
		incoming, ok := next[path]
		if !ok {
			diff[path] = ChangeManifestFile{Kind: "deleted", Path: path, BeforeHash: file.ContentHash}
			continue
		}
		if incoming.ContentHash != file.ContentHash {
			diff[path] = ChangeManifestFile{Kind: "modified", Path: path, BeforeHash: file.ContentHash, AfterHash: incoming.ContentHash}
		}
	}
	for path, file := range next {
		if _, ok := old[path]; !ok {
			diff[path] = ChangeManifestFile{Kind: "added", Path: path, AfterHash: file.ContentHash}
		}
	}
	if len(diff) != len(changes.Files) {
		return semantic("changeManifest.files", "Change manifest must enumerate the complete selected snapshot content hash diff")
	}
	for _, file := range changes.Files {
		if expected, ok := diff[file.Path]; !ok || expected != file {
			return semantic("changeManifest.files", "Change does not match the selected snapshot content hash diff")
		}
	}
	return nil
}

func incrementalManifestFiles(files []ManifestFile) (map[string]ManifestFile, error) {
	if len(files) > MaxManifestFiles {
		return nil, limitFault("Manifest file limit exceeded")
	}
	out := make(map[string]ManifestFile, len(files))
	for _, file := range files {
		if _, duplicate := out[file.Path]; duplicate || !validPath(file.Path) || !validHash(file.ContentHash) {
			return nil, semantic("manifest.files", "Manifest files require normalized unique paths and exact content hashes")
		}
		out[file.Path] = file
	}
	return out, nil
}

func incrementalChangedFiles(before SourceSnapshot, after SnapshotManifest) map[string]bool {
	old := map[string]ManifestFile{}
	next := map[string]ManifestFile{}
	changed := map[string]bool{}
	for _, file := range before.Files {
		old[file.Path] = file
	}
	for _, file := range after.Files {
		next[file.Path] = file
	}
	for path, file := range old {
		incoming, ok := next[path]
		if !ok || file.ContentHash != incoming.ContentHash || file.AnalysisStatus != incoming.AnalysisStatus || file.FileType != incoming.FileType {
			changed[path] = true
		}
	}
	for path := range next {
		if _, ok := old[path]; !ok {
			changed[path] = true
		}
	}
	return changed
}

func validateIncrementalPolicy(in BeginImportInput) error {
	switch in.SyncPolicy {
	case WholeSourcePolicy:
		if in.ChangeManifest != nil {
			return semantic("changeManifest", "Whole source imports cannot include a change manifest")
		}
	case IncrementalSourcePolicy:
		if in.SourceScope == nil || in.SourceScope.Kind != "reconcile" || in.ProfileExtension != nil {
			return reconciliationFault("backend_unsupported_scope", "Incremental imports require an existing compatible partition without profile extension")
		}
		if in.ChangeManifest == nil {
			return semantic("changeManifest", "Incremental imports require a complete change manifest")
		}
		return validateChangeManifestShape(*in.ChangeManifest)
	default:
		return reconciliationFault("backend_unsupported_scope", "Unsupported source synchronization policy")
	}
	return nil
}
