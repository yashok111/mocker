package backendportable

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/backendblob"
	"slices"
	"strconv"
	"time"
	"uuid"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func (s *Service) Preview(ctx context.Context, id string, in PreviewInput) (*PreviewResult, error) {
	request := struct {
		Version  int64
		Name     string
		Mappings []bm.PortableArtifactMapping
	}{in.ExpectedVersion, in.Name, in.ArtifactMappings}
	return portableMutation(ctx, s, id, "preview", in.IdempotencyKey, request, func(tx *sql.Tx) (*PreviewResult, string, error) {
		session, manifest, err := loadSession(ctx, tx, id)
		if err != nil {
			return nil, "", err
		}
		if err := requireVersion(session, in.ExpectedVersion); err != nil {
			return nil, "", err
		}
		if session.Direction != "import" || (session.State != "staging" && session.State != "ready") {
			return nil, "", fault(409, "Import is not previewable")
		}
		if len(in.ArtifactMappings) > 20 {
			return nil, "", fault(413, "At most 20 exact local artifact mappings per import")
		}
		records, err := loadRecords(ctx, tx, id, manifest)
		if err != nil {
			return nil, "", err
		}
		model, err := decodeModel(records, *manifest)
		if err != nil {
			return nil, "", err
		}
		if err := validateManifestSchemas(*model, *manifest); err != nil {
			return nil, "", err
		}
		if err := normalizePortableClosure(ctx, model, manifest.Selection); err != nil {
			return nil, "", err
		}
		installation, err := s.models.InstallationID(ctx)
		if err != nil {
			return nil, "", err
		}
		remap := bm.PortableRemap{OriginInstallationID: manifest.OriginInstallationID, InstallationID: installation, IDs: []bm.PortableMapping{}, Artifacts: in.ArtifactMappings}
		pins, err := bm.PortableArtifactPins(*model, manifest.OriginInstallationID)
		if err != nil {
			return nil, "", err
		}
		for _, mapping := range in.ArtifactMappings {
			if !slices.Contains(pins, mapping.Origin) {
				return nil, "", fault(422, "Artifact mapping is outside exported exact owner roster")
			}
		}
		ids, err := bm.PortableModelIdentities(*model)
		if err != nil {
			return nil, "", err
		}
		var oldRaw *string
		if err := tx.QueryRowContext(ctx, `SELECT preview FROM backend_portable_sessions WHERE id=?`, id).Scan(&oldRaw); err != nil {
			return nil, "", err
		}
		previous := map[bm.PortableIdentity]string{}
		if oldRaw != nil {
			var old preparedImport
			if err := json.Unmarshal([]byte(*oldRaw), &old, json.RejectUnknownMembers(true)); err != nil {
				return nil, "", err
			}
			for _, entry := range old.Remap.IDs {
				previous[entry.Origin] = entry.LocalID
			}
		}
		for _, identity := range ids {
			next := previous[identity]
			if next == "" {
				next = uuid.NewV7().String()
			}
			remap.IDs = append(remap.IDs, bm.PortableMapping{Origin: identity, LocalID: next})
		}
		mapped, err := bm.RemapPortableModel(*model, remap)
		if err != nil {
			return nil, "", err
		}
		mapped.Project.CreatedAt = time.Now().UTC()
		mapped.Project.UpdatedAt = mapped.Project.CreatedAt
		if in.Name != "" {
			mapped.Project.Name = in.Name
		}
		if _, err := tx.ExecContext(ctx, `SAVEPOINT portable_preview_owner_validation`); err != nil {
			return nil, "", err
		}
		prepared, ownerErr := s.models.ImportPortableModelTx(ctx, tx, *mapped, bm.PortableImportOptions{Remap: remap, OriginProjectID: model.Project.ID})
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO portable_preview_owner_validation`); err != nil {
			return nil, "", err
		}
		if _, err := tx.ExecContext(ctx, `RELEASE portable_preview_owner_validation`); err != nil {
			return nil, "", err
		}
		if ownerErr != nil {
			return nil, "", ownerErr
		}
		localRecords, err := modelRecords(*prepared, installation)
		if err != nil {
			return nil, "", err
		}
		idMap, err := makeIDMap(records, localRecords, remap, model.Project.ID, prepared.Project.ID)
		if err != nil {
			return nil, "", err
		}
		outputHash, err := DocumentHash(prepared)
		if err != nil {
			return nil, "", err
		}
		candidate, err := DocumentHash(struct {
			Domain, Session, Manifest, Output string
			Remap                             bm.PortableRemap
		}{"backend-portable-candidate-v1", id, session.ManifestHash, outputHash, remap})
		if err != nil {
			return nil, "", err
		}
		target, err := bm.PortableTargetPins(*prepared, prepared.Target)
		if err != nil {
			return nil, "", err
		}
		unresolved, err := bm.PortableArtifactPins(*prepared, installation)
		if err != nil {
			return nil, "", err
		}
		unresolved = slices.DeleteFunc(unresolved, func(p bm.NamespacedArtifactPin) bool { return p.Namespace.Scope != "foreign" })
		session.State = "ready"
		if err := advanceSession(ctx, tx, session); err != nil {
			return nil, "", err
		}
		result := PreviewResult{Session: *session, CandidateHash: candidate, ProjectID: prepared.Project.ID, Target: prepared.Target, TargetHash: target.TargetHash, IDMap: idMap, Unresolved: unresolved, RecordCount: len(records)}
		stored := preparedImport{Input: *mapped, Remap: remap, OutputHash: outputHash, Result: result}
		raw, err := json.Marshal(stored)
		if err != nil {
			return nil, "", err
		}
		if len(raw) > MaxBundleBytes {
			return nil, "", fault(413, "Prepared import exceeds bounded storage")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE backend_portable_sessions SET candidate_hash=?,preview=? WHERE id=?`, candidate, string(raw), id); err != nil {
			return nil, "", err
		}
		return &result, id, nil
	})
}

func (s *Service) Commit(ctx context.Context, id string, in CommitInput) (*CommitResult, error) {
	request := struct {
		Version   int64
		Candidate string
	}{in.ExpectedVersion, in.CandidateHash}
	return portableMutation(ctx, s, id, "commit", in.IdempotencyKey, request, func(tx *sql.Tx) (*CommitResult, string, error) {
		session, manifest, err := loadSession(ctx, tx, id)
		if err != nil {
			return nil, "", err
		}
		if err := requireVersion(session, in.ExpectedVersion); err != nil {
			return nil, "", err
		}
		if session.Direction != "import" || session.State != "ready" || !validHash(in.CandidateHash) {
			return nil, "", fault(409, "Preview an exact ready candidate before commit")
		}
		var raw, candidate string
		if err := tx.QueryRowContext(ctx, `SELECT preview,candidate_hash FROM backend_portable_sessions WHERE id=?`, id).Scan(&raw, &candidate); err != nil {
			return nil, "", err
		}
		if candidate != in.CandidateHash {
			return nil, "", fault(409, "Portable candidate differs")
		}
		var prepared preparedImport
		if err := json.Unmarshal([]byte(raw), &prepared, json.RejectUnknownMembers(true)); err != nil {
			return nil, "", err
		}
		records, err := loadRecords(ctx, tx, id, manifest)
		if err != nil {
			return nil, "", err
		}
		// The project is created NOW, not when it was previewed (review
		// 2026-10-06, F75): Preview's stamp is only a placeholder that
		// ImportPortableModelTx requires, and a session can sit in `ready`
		// for days. The two stamps are the only owner fields Commit changes,
		// so the preview/commit equality below compares the output with the
		// preview's stamps put back; any other drift still fails it.
		input := prepared.Input
		now := time.Now().UTC()
		input.Project.CreatedAt, input.Project.UpdatedAt = now, now
		imported, err := s.models.ImportPortableModelTx(ctx, tx, input, bm.PortableImportOptions{Remap: prepared.Remap, OriginProjectID: manifest.Selection.ProjectID, AfterStage: s.afterStage})
		if err != nil {
			return nil, "", err
		}
		compared := *imported
		compared.Project.CreatedAt, compared.Project.UpdatedAt = prepared.Input.Project.CreatedAt, prepared.Input.Project.UpdatedAt
		hash, err := DocumentHash(compared)
		if err != nil {
			return nil, "", err
		}
		if hash != prepared.OutputHash {
			return nil, "", fault(409, "Owner preparation differs from preview; re-preview required")
		}
		localRecords, err := modelRecords(*imported, prepared.Remap.InstallationID)
		if err != nil {
			return nil, "", err
		}
		idMap, err := makeIDMap(records, localRecords, prepared.Remap, manifest.Selection.ProjectID, imported.Project.ID)
		if err != nil {
			return nil, "", err
		}
		for i, entry := range idMap {
			origin, err := mapIdentityKey(entry.Origin, entry.Parent)
			if err != nil {
				return nil, "", err
			}
			local, err := mapIdentityKey(entry.Local, entry.LocalParent)
			if err != nil {
				return nil, "", err
			}
			document, err := canonical(entry)
			if err != nil {
				return nil, "", err
			}
			if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_portable_id_maps(project_id,session_id,identity_kind,origin_key,local_key,document) VALUES(?,?,?,?,?,?)`, imported.Project.ID, id, entry.Origin.Kind, origin, local, string(document)); err != nil {
				return nil, "", err
			}
			if i < len(records) {
				record := records[i]
				if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_portable_origins(project_id,record_kind,local_key,origin_pin,origin_hash,document) VALUES(?,?,?,?,?,?)`, imported.Project.ID, record.Kind, local, origin, record.ContentHash, string(record.Document)); err != nil {
					return nil, "", err
				}
			}
		}
		session.State = "committed"
		if _, err := tx.ExecContext(ctx, `UPDATE backend_portable_sessions SET project_id=? WHERE id=?`, imported.Project.ID, id); err != nil {
			return nil, "", err
		}
		// Nothing reads a committed session's chunks or preview again: a
		// retry is answered from the receipt (review 2026-10-06, F71).
		if err := releaseStaged(ctx, tx, id); err != nil {
			return nil, "", err
		}
		if err := advanceSession(ctx, tx, session); err != nil {
			return nil, "", err
		}
		return &CommitResult{Session: *session, Project: imported.Project, Target: imported.Target, TargetHash: prepared.Result.TargetHash, CandidateHash: candidate, IDMap: idMap}, id, nil
	})
}

func mapIdentityKey(identity Identity, parent *Identity) (string, error) {
	value := struct {
		Identity Identity  `json:"identity"`
		Parent   *Identity `json:"parent,omitzero"`
	}{identity, parent}
	raw, err := canonical(value)
	return string(raw), err
}
func mappedRecordKind(kind string) string {
	switch kind {
	case "diagram_version":
		return "diagram"
	case "diagram_view_version":
		return "diagram_view"
	case "saved_view_version":
		return "saved_view"
	}
	return kind
}
func recordAddress(record Record) (string, error) {
	key, err := record.key()
	if err != nil {
		return "", err
	}
	version := record.Identity.Version
	if record.Kind == "proposal" || record.Kind == "change_proposal" {
		version = "head"
	}
	return stringKey(record.Kind, record.Identity.ID, version, key.Parent.ID), nil
}
func stringKey(kind, id, version, parent string) string {
	raw, _ := canonical([]string{kind, id, version, parent})
	return string(raw)
}
func makeIDMap(origin, local []Record, remap bm.PortableRemap, originProject, localProject string) ([]IDMapEntry, error) {
	ids := map[bm.PortableIdentity]string{}
	for _, entry := range remap.IDs {
		ids[entry.Origin] = entry.LocalID
	}
	localByKey := map[string]Record{}
	for _, record := range local {
		key, err := recordAddress(record)
		if err != nil {
			return nil, err
		}
		if _, ok := localByKey[key]; ok {
			return nil, fault(422, "Local map record collision")
		}
		localByKey[key] = record
	}
	out := []IDMapEntry{}
	represented := map[bm.PortableIdentity]bool{}
	for _, record := range origin {
		key, err := record.key()
		if err != nil {
			return nil, err
		}
		identity := bm.PortableIdentity{Kind: mappedRecordKind(record.Kind), ID: record.Identity.ID}
		next := ids[identity]
		if next == "" {
			return nil, fault(422, "Missing typed record map")
		}
		represented[identity] = true
		var parent, localParent *Identity
		if key.Parent.ID != "" {
			p := key.Parent
			parent = &p
			q := p
			q.InstallationID, q.ProjectID = remap.InstallationID, localProject
			q.ID = ids[bm.PortableIdentity{Kind: mappedRecordKind(p.Kind), ID: p.ID}]
			if q.ID == "" {
				return nil, fault(422, "Missing mapped parent")
			}
			localParent = &q
		}
		version := record.Identity.Version
		if record.Kind == "proposal" || record.Kind == "change_proposal" {
			version = "head"
		}
		parentID := ""
		if localParent != nil {
			parentID = localParent.ID
		}
		mapped, ok := localByKey[stringKey(record.Kind, next, version, parentID)]
		if !ok {
			return nil, fault(422, "Missing mapped exact record")
		}
		out = append(out, IDMapEntry{Origin: record.Identity, Local: mapped.Identity, Parent: parent, LocalParent: localParent, OriginHash: record.ContentHash, LocalHash: mapped.ContentHash})
	}
	for _, entry := range remap.IDs {
		if represented[entry.Origin] {
			continue
		}
		version := "0"
		if slices.Contains([]string{"revision", "proposal_revision", "change_proposal_revision", "proposal", "change_proposal"}, entry.Origin.Kind) {
			version = "1"
		}
		o := Identity{InstallationID: remap.OriginInstallationID, ProjectID: originProject, Kind: entry.Origin.Kind, ID: entry.Origin.ID, Version: version}
		l := o
		l.InstallationID, l.ProjectID, l.ID = remap.InstallationID, localProject, entry.LocalID
		mapped := IDMapEntry{Origin: o, Local: l}
		if entry.Origin.Parent != "" {
			mapped.Parent = &Identity{InstallationID: o.InstallationID, ProjectID: originProject, Kind: "diagram", ID: entry.Origin.Parent, Version: "0"}
			mapped.LocalParent = &Identity{InstallationID: remap.InstallationID, ProjectID: localProject, Kind: "diagram", ID: ids[bm.PortableIdentity{Kind: "diagram", ID: entry.Origin.Parent}], Version: "0"}
		}
		if err := o.Validate(); err != nil {
			return nil, err
		}
		if err := l.Validate(); err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return out, nil
}

func portableVersion(v int64) string { return strconv.FormatInt(v, 10) }
