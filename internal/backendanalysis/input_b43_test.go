package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
)

const b43Proposal = `{"proposalId":"11111111-1111-4111-8111-111111111111","proposalRevisionId":"22222222-2222-4222-8222-222222222222"}`
const b43Common = `"limits":{},"observationMode":"none","idempotencyKey":"b43-test"`

func TestB43ClosedStarts(t *testing.T) {
	valid := []string{
		`{"kind":"change_package","changeProposal":` + b43Proposal + `,` + b43Common + `}`,
		`{"kind":"conformance","changeProposal":` + b43Proposal + `,"resultRevisionId":"33333333-3333-4333-8333-333333333333","identityMap":[],"testAttachments":[],` + b43Common + `}`,
		`{"kind":"endpoint_review","fromRevisionId":"11111111-1111-4111-8111-111111111111","toRevisionId":"22222222-2222-4222-8222-222222222222","beforeEndpointId":"33333333-3333-4333-8333-333333333333","afterEndpointId":null,` + b43Common + `}`,
	}
	for _, raw := range valid {
		var in StartInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Errorf("valid arm: %v", err)
			continue
		}
		encoded, err := canonical(in)
		if err != nil {
			t.Fatal(err)
		}
		var again StartInput
		if err = json.Unmarshal(encoded, &again); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{strings.Replace(raw, `"limits":{}`, `"limits":null`, 1), strings.Replace(raw, `"limits":{}`, `"scope":{},"limits":{}`, 1), strings.Replace(raw, `"limits":{}`, `"kind":"impact","limits":{}`, 1)} {
			if err = json.Unmarshal([]byte(bad), &again); err == nil {
				t.Errorf("accepted %s", bad)
			}
		}
	}
	for _, bad := range []string{strings.Replace(valid[1], `"identityMap":[],`, "", 1), strings.Replace(valid[1], `"testAttachments":[]`, `"testAttachments":null`, 1), strings.Replace(valid[2], `"afterEndpointId":null,`, "", 1)} {
		var in StartInput
		if json.Unmarshal([]byte(bad), &in) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestB43V1RequestOracle(t *testing.T) {
	var in StartInput
	if err := json.Unmarshal([]byte(validStart), &in); err != nil {
		t.Fatal(err)
	}
	raw, err := canonical(in)
	if err != nil {
		t.Fatal(err)
	}
	if digest(raw) != "48439af43184b5a933f3681f1cb47dd468eb49694959bfce77a4eeba3887f8b7" {
		t.Fatalf("v1 request changed: %s", raw)
	}
}

func TestB43PackageSavedInputAndDeterminism(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	raw := `{"kind":"change_package","changeProposal":{"proposalId":"` + d.Proposal.ID + `","proposalRevisionId":"` + d.Revision.ID + `"},` + b43Common + `}`
	var start StartInput
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		t.Fatal(err)
	}
	j, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.jobs.Input(t.Context(), f.project.ID, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DocumentVersion != "backend-analysis-input/v2" {
		t.Fatal("not v2")
	}
	first, err := s.engine.Analyze(t.Context(), saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.importSource("advance", "advanced", false, nil)
	second, err := s.engine.Analyze(t.Context(), saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if integrationBytes(t, first) != integrationBytes(t, second) {
		t.Fatal("package changed after source advancement")
	}
	s.graphs = nil
	replay, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil || integrationBytes(t, j) != integrationBytes(t, replay) {
		t.Fatalf("replay: %v", err)
	}
}

func TestB43PackagePublicationAndHeaderBudget(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	for _, limits := range []string{`{}`, `{"records":1}`} {
		raw := `{"kind":"change_package","changeProposal":{"proposalId":"` + d.Proposal.ID + `","proposalRevisionId":"` + d.Revision.ID + `"},"limits":` + limits + `,"observationMode":"none","idempotencyKey":"` + digest([]byte(limits)) + `"}`
		var start StartInput
		if err := json.Unmarshal([]byte(raw), &start); err != nil {
			t.Fatal(err)
		}
		j, err := s.Start(t.Context(), f.project.ID, start)
		if err != nil {
			t.Fatal(err)
		}
		claim, err := f.jobs.Claim(t.Context(), "test-claim")
		if err != nil || claim == nil {
			t.Fatalf("claim %v", err)
		}
		if err = s.execute(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
		current, err := f.jobs.Get(t.Context(), f.project.ID, j.ID)
		if err != nil || current.Status != "completed" {
			t.Fatalf("job %+v %v", current, err)
		}
		page, err := f.jobs.Results(t.Context(), f.project.ID, j.ID, ResultQuery{ResultVersion: *current.ResultVersion, Section: "findings"})
		if err != nil {
			t.Fatal(err)
		}
		var records []ResultRecord
		if err = json.Unmarshal(page.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 {
			t.Fatalf("header count %d", len(records))
		}
		var header PackageHeaderDetail
		if err = json.Unmarshal(records[0].Detail, &header); err != nil {
			t.Fatal(err)
		}
		if len(header.PackageHash) != 64 || header.Complete != (limits == `{}`) || page.Manifest.Complete != header.Complete {
			t.Fatalf("header %+v manifest %+v", header, page.Manifest)
		}
		detail, err := f.jobs.Detail(t.Context(), f.project.ID, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(detail)
		if err != nil || !strings.Contains(string(wire), `backend-analysis-context-v2`) {
			t.Fatalf("context %s %v", wire, err)
		}
	}
}

func TestB43ImmutablePayloadClosed(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	var start StartInput
	raw := `{"kind":"change_package","changeProposal":{"proposalId":"` + d.Proposal.ID + `","proposalRevisionId":"` + d.Revision.ID + `"},` + b43Common + `}`
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		t.Fatal(err)
	}
	j, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.jobs.Input(t.Context(), f.project.ID, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonical(saved)
	if err != nil {
		t.Fatal(err)
	}
	var outer map[string]jsontext.Value
	if err = json.Unmarshal(encoded, &outer); err != nil {
		t.Fatal(err)
	}
	var payload map[string]jsontext.Value
	if err = json.Unmarshal(outer["payload"], &payload); err != nil {
		t.Fatal(err)
	}
	for key := range payload {
		for _, replacement := range []jsontext.Value{nil, []byte("null")} {
			old := payload[key]
			if replacement == nil {
				delete(payload, key)
			} else {
				payload[key] = replacement
			}
			outer["payload"], _ = json.Marshal(payload)
			bad, _ := json.Marshal(outer)
			var in ImmutableInput
			if json.Unmarshal(bad, &in) == nil {
				t.Errorf("accepted missing/null %s", key)
			}
			payload[key] = old
		}
	}
	var pin map[string]jsontext.Value
	if err = json.Unmarshal(payload["basePins"], &pin); err != nil {
		t.Fatal(err)
	}
	pin["extra"] = []byte(`true`)
	payload["basePins"], _ = json.Marshal(pin)
	outer["payload"], _ = json.Marshal(payload)
	bad, _ := json.Marshal(outer)
	var in ImmutableInput
	if json.Unmarshal(bad, &in) == nil {
		t.Error("accepted unknown pin field")
	}
}

func TestB43PackageResultCertaintyUsesExistingEnvelope(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	d := f.proposal()
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	var start StartInput
	raw := `{"kind":"change_package","changeProposal":{"proposalId":"` + d.Proposal.ID + `","proposalRevisionId":"` + d.Revision.ID + `"},` + b43Common + `}`
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.jobs.Input(t.Context(), f.project.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.engine.Analyze(t.Context(), input, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range report.Snapshot.Chunks {
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Certainty != "confirmed" && record.Certainty != "possible" && record.Certainty != "unknown" {
				t.Errorf("invalid existing record certainty: %s/%s", record.Kind, record.Certainty)
			}
		}
	}
}
