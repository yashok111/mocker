package backendanalysis

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type PackageHeaderDetail struct {
	DocumentVersion string `json:"documentVersion"`
	Type            string `json:"type"`
	PackagePayload
	PackageHash string `json:"packageHash"`
	Complete    bool   `json:"complete"`
}
type PackageChangeDetail struct {
	DocumentVersion string     `json:"documentVersion"`
	Type            string     `json:"type"`
	Change          DiffChange `json:"change"`
}
type PackageEvidenceDetail struct {
	DocumentVersion string         `json:"documentVersion"`
	Type            string         `json:"type"`
	Object          ObjectAddress  `json:"object"`
	Origin          string         `json:"origin"`
	Proof           ProofReference `json:"proof"`
}
type PackageCriterionDetail struct {
	DocumentVersion string                       `json:"documentVersion"`
	Type            string                       `json:"type"`
	Criterion       backendmodel.ChangeCriterion `json:"criterion"`
}
type RecommendedCheckDetail struct {
	DocumentVersion string     `json:"documentVersion"`
	Type            string     `json:"type"`
	Check           RuleResult `json:"check"`
	Required        bool       `json:"required"`
	Origin          string     `json:"origin"`
}

const b43ResultVersion = "backend-b43-result-v1"

func analyzePackage(ctx context.Context, in *ImmutableInput, p *PackagePayload, before, after *backendmodel.EffectiveGraphSnapshot, emit func(PreparedSnapshot) error) (*TerminalSnapshot, error) {
	r := newReport(in)
	r.before, r.after = before, after
	r.services = objectServices(before, after)
	header := PackageHeaderDetail{DocumentVersion: b43ResultVersion, Type: "package_header", PackagePayload: *p, PackageHash: strings.Repeat("0", 64), Complete: false}
	headerObject := ObjectAddress{RecordType: "desired", ID: p.ChangeProposal.ProposalRevisionID}
	if !r.add("findings", headerObject, "package_header", "confirmed", 0, header) {
		return r.complete(before, after)
	}
	changes, err := structuralChanges(ctx, before, after)
	if err != nil {
		return nil, err
	}
	if err = r.addPackageChanges(ctx, changes, before, after); err != nil {
		return nil, err
	}
	for _, criterion := range after.Criteria {
		r.add("checks", ObjectAddress{RecordType: "desired", ID: criterion.Key}, "criterion", "confirmed", 0, PackageCriterionDetail{b43ResultVersion, "package_criterion", criterion})
	}
	if before.State.Revision.Coverage.Status != "complete" || after.State.Revision.Coverage.Status != "complete" {
		r.gap("source_coverage", ObjectAddress{RecordType: "source", ID: "coverage"})
	}
	for _, gap := range diagnostics(r.gaps) {
		r.add("gaps", ObjectAddress{RecordType: "diagnostic", ID: gap.ID}, "gap", "unknown", 0, gap)
	}
	header.Complete = len(r.truncations) == 0
	raw, err := canonical(in)
	if err != nil {
		return nil, err
	}
	header.PackageHash, err = packageHash(digest(raw), header, r.records)
	if err != nil {
		return nil, err
	}
	detail, err := canonical(header)
	if err != nil {
		return nil, err
	}
	var record ResultRecord
	old := r.records["findings"][0]
	if err = json.Unmarshal(old, &record); err != nil {
		return nil, err
	}
	record.Detail = detail
	record.ID = digest(append([]byte("findings:"), detail...))
	final, err := canonical(record)
	if err != nil {
		return nil, err
	}
	r.bytes += int64(len(final) - len(old))
	r.records["findings"][0] = final
	return r.complete(before, after)
}
func packageHash(inputHash string, header PackageHeaderDetail, records map[string][]jsontext.Value) (string, error) {
	raw, err := canonical(header)
	if err != nil {
		return "", err
	}
	var h map[string]jsontext.Value
	if err = json.Unmarshal(raw, &h); err != nil {
		return "", err
	}
	delete(h, "type")
	delete(h, "documentVersion")
	delete(h, "packageHash")
	ordered := []struct {
		Section string         `json:"section"`
		Detail  jsontext.Value `json:"detail"`
	}{}
	for _, section := range sections {
		details := []jsontext.Value{}
		for _, raw := range records[section] {
			var record ResultRecord
			if err = json.Unmarshal(raw, &record); err != nil {
				return "", err
			}
			var tag struct {
				Type string `json:"type"`
			}
			if err = json.Unmarshal(record.Detail, &tag); err != nil {
				return "", err
			}
			if tag.Type == "package_header" {
				continue
			}
			details = append(details, record.Detail)
		}
		slices.SortFunc(details, func(a, b jsontext.Value) int { return bytes.Compare(a, b) })
		for _, detail := range details {
			ordered = append(ordered, struct {
				Section string         `json:"section"`
				Detail  jsontext.Value `json:"detail"`
			}{section, detail})
		}
	}
	return requestHash(struct {
		DocumentVersion string `json:"documentVersion"`
		InputHash       string `json:"inputHash"`
		Content         any    `json:"content"`
	}{"backend-change-package/v1", inputHash, struct {
		Header  any `json:"header"`
		Records any `json:"records"`
	}{h, ordered}})
}

func (r *reportBuilder) addPackageChanges(ctx context.Context, changes []DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) error {
	var err error
	for _, change := range changes {
		if err = ctx.Err(); err != nil {
			return err
		}
		if !r.trackChange(change.Object) {
			continue
		}
		r.covered[change.Object] = true
		r.add("changes", change.Object, change.Kind, "confirmed", 0, PackageChangeDetail{b43ResultVersion, "package_change", change})
		r.addPackageEvidence(change, before, after)
		if change.Facet == "behavior" {
			check := ruleFor(change)
			for _, side := range []string{"before", "after"} {
				if side == "before" && change.Operation == "added" || side == "after" && change.Operation == "removed" {
					continue
				}
				g := before
				if side == "after" {
					g = after
				}
				check.Evidence = append(check.Evidence, proofReference(side, g, recordProof(g, change.Object)))
			}
			r.add("checks", change.Object, change.Kind, check.Certainty, 0, RecommendedCheckDetail{b43ResultVersion, "recommended_check", check, false, "analysis_rule"})
		}
	}
	return nil
}

func (r *reportBuilder) addPackageEvidence(change DiffChange, before, after *backendmodel.EffectiveGraphSnapshot) {
	for _, side := range []string{"before", "after"} {
		if (side == "before" && change.Operation == "added") || (side == "after" && change.Operation == "removed") {
			continue
		}
		g, origin := before, "baseline"
		if side == "after" {
			g, origin = after, "intent"
		}
		proof := recordProof(g, change.Object)
		if !supported(proof) {
			r.gap("unsupported_change_proof", change.Object)
		}
		certainty := "unknown"
		if supported(proof) {
			certainty = "confirmed"
		}
		r.add("witnesses", change.Object, change.Kind, certainty, 0, PackageEvidenceDetail{b43ResultVersion, "package_evidence", change.Object, origin, proofReference(side, g, proof)})
	}
}
