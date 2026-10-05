package admin

import (
	"bytes"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendB43PublicStartsAndFrozenQueries(t *testing.T) {
	s := loopbackTestServer(t, nil)
	jobs := backendanalysis.NewRepo(s.db)
	s.SetBackendAnalysis(backendanalysis.NewService(jobs, s.backendRepo, backendanalysis.NewEngine(s.backendRepo, nil)), jobs)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "B43", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	source := b42Source(t, s, *p, "", "source", "System", "5")
	base := "/api/backend-projects/" + p.ID
	var draft backendmodel.ChangeProposalDetail
	b41Call(t, s, "POST", base+"/change-proposals", backendmodel.CreateChangeProposalInput{Name: "Draft", BaseRevisionID: source.Revision.ID, IdempotencyKey: "draft"}, 200, &draft)
	proposal := `{"proposalId":"` + draft.Proposal.ID + `","proposalRevisionId":"` + draft.Revision.ID + `"}`
	for _, kind := range []string{"change_package", "conformance"} {
		raw := `{"kind":"` + kind + `","changeProposal":` + proposal + `,"limits":{"resultBytes":1048576},"observationMode":"none","idempotencyKey":"` + kind + `"`
		if kind == "conformance" {
			raw += `,"resultRevisionId":"` + source.Revision.ID + `","identityMap":[],"testAttachments":[]`
		}
		raw += "}"
		if kind == "conformance" {
			for _, bad := range []string{strings.Replace(raw, `"identityMap":[],`, "", 1), strings.Replace(raw, `"testAttachments":[]`, `"testAttachments":null`, 1), strings.Replace(raw, `"changeProposal":`+proposal, `"changeProposal":null`, 1)} {
				b41Call(t, s, "POST", base+"/analyses", bad, 400, nil)
			}
		}
		first := b41Call(t, s, "POST", base+"/analyses", raw, 202, nil)
		var job backendanalysis.Job
		if err := json.Unmarshal(first, &job); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, b41Call(t, s, "POST", base+"/analyses", raw, 202, nil)) {
			t.Fatal("same-key 202 changed")
		}
		b41Call(t, s, "GET", base+"/analyses?kind="+kind, nil, 200, nil)
		b41Call(t, s, "GET", base+"/analyses/"+job.ID, nil, 200, nil)
		b41Call(t, s, "GET", base+"/analyses/"+job.ID+"/results?resultVersion=1&section=checks", nil, 409, nil)
		for _, bad := range []string{strings.Replace(raw, `"limits":{`, `"limits":null,"irrelevant":{`, 1), strings.Replace(raw, `"observationMode":"none"`, `"observationMode":"none","observationMode":"none"`, 1), strings.Replace(raw, `"limits":{"resultBytes":1048576}`, `"limits":{},"scope":{}`, 1)} {
			b41Call(t, s, "POST", base+"/analyses", bad, 400, nil)
		}
	}
	lifecycle := base + "/change-proposals/" + draft.Proposal.ID + "/lifecycle"
	for _, v := range []string{"1.0", "1e0", "9223372036854775808", "null"} {
		raw := `{"expectedVersion":` + v + `,"proposalRevisionId":"` + draft.Revision.ID + `","action":"archive","idempotencyKey":"bad"}`
		b41Call(t, s, "POST", lifecycle, raw, 400, nil)
	}
	b41Call(t, s, "POST", lifecycle, `{"expectedVersion":9223372036854775807,"proposalRevisionId":"`+draft.Revision.ID+`","action":"archive","idempotencyKey":"int64"}`, 409, nil)
	for _, status := range []string{"implemented", "archived"} {
		b41Call(t, s, "GET", base+"/change-proposals?status="+status, nil, 200, nil)
	}
}

func TestBackendB43Capabilities(t *testing.T) {
	caps := readB41Capabilities(t, loopbackTestServer(t, nil))
	for _, feature := range []string{"backend-change-package", "backend-conformance", "backend-endpoint-review", "backend-change-implemented", "backend-change-archive", "backend-change-unarchive"} {
		if !slices.Contains(caps.Features, feature) {
			t.Errorf("missing capability %s", feature)
		}
	}
}

func TestBackendB43AnalysisSupportNegotiation(t *testing.T) {
	s := loopbackTestServer(t, nil)
	var caps struct {
		AnalysisSupport struct {
			DocumentVersion       string   `json:"documentVersion"`
			DocumentVersions      []string `json:"documentVersions"`
			InputDocumentVersions []string `json:"inputDocumentVersions"`
			RuleSetVersion        string   `json:"ruleSetVersion"`
			RuleSetVersions       []string `json:"ruleSetVersions"`
			Kinds                 []string `json:"kinds"`
		} `json:"analysisSupport"`
	}
	b41Call(t, s, "GET", "/api/backend-projects/capabilities", nil, 200, &caps)
	v := caps.AnalysisSupport
	if v.DocumentVersion != "backend-analysis-context-v1" || v.RuleSetVersion != "b42-rules/v1" || !slices.Equal(v.DocumentVersions, []string{"backend-analysis-context-v1", "backend-analysis-context-v2"}) || !slices.Equal(v.InputDocumentVersions, []string{"backend-analysis-input/v1", "backend-analysis-input/v2"}) || !slices.Equal(v.RuleSetVersions, []string{"b42-rules/v1", "b43-rules/v1"}) || !slices.Contains(v.Kinds, "endpoint_review") {
		t.Fatal(v)
	}
}
