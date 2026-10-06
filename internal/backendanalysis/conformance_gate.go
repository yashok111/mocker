package backendanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"

	"github.com/yashok111/mocker/internal/backendmodel"
)

var _ backendmodel.ConformanceEvidenceReader = (*Repo)(nil)

func (r *Repo) ReadConformanceEvidence(ctx context.Context, pid string, ref backendmodel.AnalysisReportRef) (*backendmodel.ConformanceEvidence, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	header, err := readConformanceGateHeader(ctx, tx, pid, ref)
	if err != nil {
		return nil, err
	}
	job, payload, manifest, manifestRaw := header.job, header.payload, header.manifest, header.raw
	var bytesToRead int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(length(CAST(items_json AS BLOB))),0) FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=? AND sequence<=?`, pid, ref.JobID, manifest.HighWaterSequence).Scan(&bytesToRead); err != nil {
		return nil, err
	}
	if bytesToRead > maxResultBytes {
		return nil, fault(413, "result_limit", "Conformance gate input exceeds result byte limit")
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,section,content_hash,items_json,record_count,chunk_bytes FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=? AND sequence<=? ORDER BY sequence`, pid, ref.JobID, manifest.HighWaterSequence)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	snapshot := PreparedSnapshot{Manifest: manifest}
	collector := &conformanceGateCollector{criteria: []backendmodel.ConformanceCriterionEvidence{}, seen: map[string]bool{}}
	var total int64
	for rows.Next() {
		var chunk ResultChunk
		var count int
		var size int64
		if err = rows.Scan(&chunk.Sequence, &chunk.Section, &chunk.ContentHash, &chunk.ItemsJSON, &count, &size); err != nil {
			return nil, err
		}
		total += size
		if size != int64(len(chunk.ItemsJSON)) || total > maxResultBytes || digest(chunk.ItemsJSON) != chunk.ContentHash {
			return nil, fault(409, "gate_conflict", "Conformance chunk bytes differ")
		}
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			return nil, err
		}
		if len(records) != count {
			return nil, fault(409, "gate_conflict", "Conformance chunk count differs")
		}
		if err = collector.consume(chunk.Section, records); err != nil {
			return nil, err
		}
		snapshot.Chunks = append(snapshot.Chunks, chunk)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	verified, err := prepareSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(verified.ManifestJSON, manifestRaw) {
		return nil, fault(409, "gate_conflict", "Conformance content does not reproduce saved manifest")
	}
	return collector.evidence(pid, ref, job, payload, manifest)
}

type conformanceGateHeader struct {
	job      *Job
	payload  *ConformancePayload
	manifest ResultManifest
	raw      []byte
}

func readConformanceGateHeader(ctx context.Context, tx *sql.Tx, pid string, ref backendmodel.AnalysisReportRef) (*conformanceGateHeader, error) {
	job, err := readJob(ctx, tx, pid, ref.JobID)
	if err != nil {
		return nil, err
	}
	if !exactCompletedConformance(job, ref) {
		return nil, fault(409, "gate_conflict", "Exact completed conformance required")
	}
	inputRaw, err := inputBytes(ctx, tx, pid, ref.JobID)
	if err != nil {
		return nil, err
	}
	var input ImmutableInput
	if err = json.Unmarshal(inputRaw, &input); err != nil {
		return nil, err
	}
	if digest(inputRaw) != ref.InputHash || input.ProjectID != pid || input.V2 == nil || input.Kind != "conformance" {
		return nil, fault(409, "gate_conflict", "Conformance input differs")
	}
	payload, ok := input.V2.Payload.(*ConformancePayload)
	if !ok {
		return nil, fault(409, "gate_conflict", "Invalid conformance payload")
	}
	var manifestRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT document FROM backend_analysis_manifests_documents WHERE project_id=? AND job_id=? AND result_version=?`, pid, ref.JobID, ref.ResultVersion).Scan(&manifestRaw); err != nil {
		return nil, err
	}
	var manifest ResultManifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, err
	}
	if manifest.JobID != ref.JobID || manifest.ResultVersion != ref.ResultVersion || manifest.AnalysisInputHash != ref.InputHash || manifest.SemanticResultHash != ref.ResultHash || manifest.RuleSetVersion != input.RuleSetVersion || manifest.TraversalVersion != input.TraversalVersion {
		return nil, fault(409, "gate_conflict", "Conformance manifest pins differ")
	}
	return &conformanceGateHeader{job: job, payload: payload, manifest: manifest, raw: manifestRaw}, nil
}

type conformanceGateCollector struct {
	criteria []backendmodel.ConformanceCriterionEvidence
	seen     map[string]bool
	summary  *ConformanceSummaryDetail
}

func (c *conformanceGateCollector) consume(section string, records []ResultRecord) error {
	var err error
	for _, record := range records {
		var tag struct {
			Type            string `json:"type"`
			DocumentVersion string `json:"documentVersion"`
		}
		if err = json.Unmarshal(record.Detail, &tag); err != nil {
			return err
		}
		if section == "checks" {
			if err = c.consumeCriterion(record, tag.Type, tag.DocumentVersion); err != nil {
				return err
			}
		}
		if tag.Type == "conformance_summary" {
			if section != "findings" || tag.DocumentVersion != b43ResultVersion || c.summary != nil {
				return fault(409, "gate_conflict", "Invalid conformance c.summary")
			}
			if _, err = closed(record.Detail, []string{"documentVersion", "type", "changeProposal", "draftHash", "resultRevisionId", "resultSemanticHash", "criteriaCount", "satisfiedCount", "violatedCount", "unverifiedCount", "requiredSatisfied", "behaviorStatus"}, nil); err != nil {
				return err
			}
			c.summary = new(ConformanceSummaryDetail)
			if err = decode(record.Detail, c.summary); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *conformanceGateCollector) evidence(pid string, ref backendmodel.AnalysisReportRef, job *Job, payload *ConformancePayload, manifest ResultManifest) (*backendmodel.ConformanceEvidence, error) {
	criteria, summary := c.criteria, c.summary
	if summary == nil || summary.ChangeProposal != payload.ChangeProposal || summary.DraftHash != payload.DraftPins.EffectiveSemanticHash || summary.ResultRevisionID != payload.ResultRevisionID || summary.ResultSemanticHash != payload.ResultPins.BaseSemanticHash || summary.BehaviorStatus != "unverified" {
		return nil, fault(409, "gate_conflict", "Conformance summary does not match exact input")
	}
	satisfied, violated, unverified := 0, 0, 0
	requiredSatisfied := true
	for _, row := range criteria {
		switch row.Outcome {
		case "satisfied":
			satisfied++
		case "violated":
			violated++
		default:
			unverified++
		}
		if row.Required && row.Outcome != "satisfied" {
			requiredSatisfied = false
		}
	}
	if manifest.Complete && (summary.CriteriaCount != len(criteria) || summary.SatisfiedCount != satisfied || summary.ViolatedCount != violated || summary.UnverifiedCount != unverified || summary.RequiredSatisfied != requiredSatisfied) {
		return nil, fault(409, "gate_conflict", "Conformance summary differs from immutable criterion rows")
	}
	out := &backendmodel.ConformanceEvidence{ProjectID: pid, Kind: job.Kind, Status: job.Status, Report: ref, ChangeProposal: payload.ChangeProposal, BaseRevisionID: payload.BaseRevisionID, DraftHash: payload.DraftPins.EffectiveSemanticHash, ResultRevisionID: payload.ResultRevisionID, ResultSemanticHash: payload.ResultPins.BaseSemanticHash, BasePins: payload.BasePins, DraftPins: payload.DraftPins, ResultPins: payload.ResultPins, BaseSource: payload.BaseSource, DraftSource: payload.DraftSource, ResultSource: payload.ResultSource, EvidencePins: payload.EvidencePins, Complete: manifest.Complete, RuleSetVersion: manifest.RuleSetVersion, TraversalVersion: manifest.TraversalVersion, Criteria: criteria, BehaviorStatus: summary.BehaviorStatus}
	for _, reason := range manifest.TruncationReasons {
		out.TruncationReasons = append(out.TruncationReasons, reason.Code)
	}
	return out, nil
}

func (c *conformanceGateCollector) consumeCriterion(record ResultRecord, typ, version string) error {
	var err error

	if typ != "conformance_criterion" || version != b43ResultVersion {
		return fault(409, "gate_conflict", "Invalid conformance criterion detail")
	}
	if _, err = closed(record.Detail, []string{"documentVersion", "type", "criterionKey", "criterionKind", "required", "outcome", "reason", "basis", "proposalObject", "sourceObject", "deletionBasis"}, nil); err != nil {
		// Object references are explicitly nullable in this closed arm.
		var nullable map[string]jsontext.Value
		if e := json.Unmarshal(record.Detail, &nullable); e != nil {
			return e
		}
		for _, key := range []string{"proposalObject", "sourceObject"} {
			if value, exists := nullable[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				nullable[key] = []byte(`{"recordType":"node","id":"placeholder"}`)
			}
		}
		normalized, e := json.Marshal(nullable)
		if e != nil {
			return e
		}
		if _, err = closed(normalized, []string{"documentVersion", "type", "criterionKey", "criterionKind", "required", "outcome", "reason", "basis", "proposalObject", "sourceObject", "deletionBasis"}, nil); err != nil {
			return err
		}
	}
	var row ConformanceCriterionDetail
	if err = decode(record.Detail, &row); err != nil {
		return err
	}
	if row.CriterionKind == "runtime_check" && row.Outcome != "unverified" {
		return fault(409, "gate_conflict", "Static conformance cannot establish runtime outcomes")
	}
	if c.seen[row.CriterionKey] || !slices.Contains([]string{"satisfied", "violated", "unverified"}, row.Outcome) {
		return fault(409, "gate_conflict", "Duplicate or invalid criterion outcome")
	}
	c.seen[row.CriterionKey] = true
	c.criteria = append(c.criteria, backendmodel.ConformanceCriterionEvidence{Key: row.CriterionKey, Kind: row.CriterionKind, Required: row.Required, Outcome: row.Outcome})
	return nil
}

func exactCompletedConformance(job *Job, ref backendmodel.AnalysisReportRef) bool {
	return job.Kind == "conformance" && job.Status == "completed" && job.AnalysisInputHash == ref.InputHash && job.ResultVersion != nil && *job.ResultVersion == ref.ResultVersion
}
