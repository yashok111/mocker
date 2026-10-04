package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"uuid"
)

func TestEffectiveGraphReadTargetXOR(t *testing.T) {
	id, draft := uuid.NewV7().String(), uuid.NewV7().String()
	tags := []string{
		`"revisionId":"` + id + `"`,
		`"proposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"}`,
		`"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"}`,
		`"importCandidate":{"importId":"` + id + `","importVersion":1,"candidateHash":"` + strings.Repeat("a", 64) + `"}`,
	}
	for _, tag := range tags {
		var target BackendReadTarget
		raw := []byte("{" + tag + "}")
		if err := json.Unmarshal(raw, &target); err != nil {
			t.Fatalf("valid exact target %s: %v", tag, err)
		}
		encoded, err := json.Marshal(target)
		if err != nil || string(encoded) != string(raw) {
			t.Fatalf("target lost its exact tag or pins: %s -> %s (%v)", raw, encoded, err)
		}
	}
	for i, left := range tags {
		for _, right := range tags[i+1:] {
			var target BackendReadTarget
			if err := json.Unmarshal([]byte("{"+left+","+right+"}"), &target); err == nil {
				t.Fatalf("accepted mixed target %s and %s", left, right)
			}
		}
	}
	for _, raw := range []string{
		`{}`, `null`, `{"revisionId":null}`, `{"revisionId":""}`, `{"revisionId":"not-a-uuid"}`,
		`{"proposal":null}`, `{"proposal":{}}`, `{"changeProposal":null}`, `{"changeProposal":{}}`,
		`{"importCandidate":null}`, `{"importCandidate":{}}`, `{"unexpected":true}`,
		`{"revisionId":"` + id + `","revisionId":"` + id + `"}`,
		`{"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `","head":true}}`,
	} {
		var target BackendReadTarget
		if err := json.Unmarshal([]byte(raw), &target); err == nil {
			t.Fatalf("accepted invalid target %s", raw)
		}
	}
}

func TestEffectiveCandidateReadTargetExactVersion(t *testing.T) {
	id := uuid.NewV7().String()
	for _, version := range []string{"null", "0", "-1", "1.1", `"1"`, "true"} {
		raw := `{"importCandidate":{"importId":"` + id + `","importVersion":` + version + `,"candidateHash":"` + strings.Repeat("a", 64) + `"}}`
		var target BackendReadTarget
		if err := json.Unmarshal([]byte(raw), &target); err == nil {
			t.Fatalf("accepted inexact candidate version %s", version)
		}
	}
}

func TestEffectiveCandidateReadTargetExactInt64Backend(t *testing.T) {
	id := uuid.NewV7().String()
	for _, version := range []string{"9007199254740993", "9223372036854775807"} {
		raw := `{"importCandidate":{"importId":"` + id + `","importVersion":` + version + `,"candidateHash":"` + strings.Repeat("a", 64) + `"}}`
		var target BackendReadTarget
		if err := json.Unmarshal([]byte(raw), &target); err != nil {
			t.Fatalf("exact int64 rejected %s: %v", version, err)
		}
		out, err := json.Marshal(target)
		if err != nil || string(out) != raw {
			t.Fatalf("backend lost int64 digits: %s %v", out, err)
		}
	}
	for _, version := range []string{"9223372036854775808", "-9223372036854775809"} {
		raw := `{"importCandidate":{"importId":"` + id + `","importVersion":` + version + `,"candidateHash":"` + strings.Repeat("a", 64) + `"}}`
		var target BackendReadTarget
		if err := json.Unmarshal([]byte(raw), &target); err == nil {
			t.Fatal("int64 overflow admitted")
		}
	}
}

func TestEffectiveSpecializedDecoderKeepsTargetAndPresence(t *testing.T) {
	id, draft := uuid.NewV7().String(), uuid.NewV7().String()
	for _, query := range []string{
		`{"revisionId":"` + id + `","changeProposal":null,"view":"entrypoints"}`,
		`{"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"},"view":"entrypoints","limit":0}`,
		`{"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"},"view":"entrypoints","flowId":""}`,
	} {
		var input FlowQueryInput
		if err := json.Unmarshal([]byte(query), &input); err == nil {
			t.Fatalf("advanced flow erased invalid presence: %s", query)
		}
	}
	for _, query := range []string{`{"revisionId":"` + id + `","changeProposal":null}`, `{"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"},"limit":0}`, `{"changeProposal":{"proposalId":"` + id + `","proposalRevisionId":"` + draft + `"},"sourceNodeId":null}`} {
		var input APIArtifactQueryInput
		if err := json.Unmarshal([]byte(query), &input); err == nil {
			t.Fatalf("advanced API erased invalid presence: %s", query)
		}
	}
}
