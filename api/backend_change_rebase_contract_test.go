package api

import (
	"strings"
	"testing"
)

func TestBackendChangeRebaseClosedRequests(t *testing.T) {
	v := lineageSchemaValidator(t, "PreviewBackendChangeProposalRebaseRequest")
	raw := `{"expectedVersion":9007199254740993,"proposalRevisionId":"` + source6ContractID + `","newBaseRevisionId":"` + source6ContractID + `","identityResolutions":[],"resolutions":[],"repairCommands":[]}`
	if err := v(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, "9007199254740993", "9223372036854775808", 1), strings.Replace(raw, `,"resolutions":[]`, "", 1), strings.Replace(raw, `"repairCommands":[]`, `"repairCommands":null`, 1)} {
		if v(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
	_ = lineageSchemaValidator(t, "ApplyBackendChangeProposalRebaseRequest")
}
func TestBackendChangeLifecycleClosedRequest(t *testing.T) {
	v := lineageSchemaValidator(t, "ApplyBackendChangeProposalLifecycleRequest")
	raw := `{"expectedVersion":9223372036854775807,"proposalRevisionId":"` + source6ContractID + `","action":"ready","idempotencyKey":"ready","report":{"jobId":"` + source6ContractID + `","resultVersion":9007199254740993,"inputHash":"` + strings.Repeat("a", 64) + `","resultHash":"` + strings.Repeat("b", 64) + `"},"acknowledgedGapIds":[]}`
	if err := v(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, `,"acknowledgedGapIds":[]`, "", 1), strings.Replace(raw, `"acknowledgedGapIds":[]`, `"acknowledgedGapIds":null`, 1), strings.Replace(raw, `"action":"ready"`, `"action":"merged"`, 1)} {
		if v(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestBackendChangeRebasePresenceCarryAndAuthorship(t *testing.T) {
	value := lineageSchemaValidator(t, "BackendChangeRebaseValue")
	for _, raw := range []string{`{"presence":"absent_record"}`, `{"presence":"absent_property"}`, `{"presence":"null","value":null}`, `{"presence":"value","value":{"status":"known","value":9007199254740993}}`} {
		if err := value(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{`{"presence":"absent_record","value":null}`, `{"presence":"null"}`, `{"presence":"value","value":null}`, `{"presence":"absent"}`} {
		if value(raw) == nil {
			t.Fatal("accepted", raw)
		}
	}
	carry := `{"kind":"carried_source_identity","source":{"recordType":"node","id":"` + source6ContractID + `","repositoryId":"` + source6ContractID + `","providerNamespace":"provider","externalKey":"old","assertionHash":"` + strings.Repeat("a", 64) + `"},"basis":{"revisionId":"` + source6ContractID + `","semanticHash":"` + strings.Repeat("b", 64) + `"}}`
	target := lineageSchemaValidator(t, "BackendChangeIdentityTarget")
	if err := target(carry); err != nil {
		t.Fatal(err)
	}
	if target(strings.Replace(carry, "carried_source_identity", "source_identity", 1)) == nil {
		t.Fatal("current identity accepted historical basis")
	}
	origin := lineageSchemaValidator(t, "BackendChangeOrigin")
	raw := `{"kind":"intent","rebaseResolution":{"proposalRevisionId":"` + source6ContractID + `","resolutionId":"` + strings.Repeat("a", 64) + `","reason":"choice"}}`
	if err := origin(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(raw, `"intent"`, `"base"`, 1), strings.Replace(raw, `"kind":"intent"`, `"kind":"intent","commandId":"`+source6ContractID+`"`, 1), strings.Replace(raw, `"kind":"intent"`, `"kind":"intent","reason":"duplicate authorship"`, 1)} {
		if origin(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
	replacement := lineageSchemaValidator(t, "BackendChangeRebaseReplacementValue")
	if replacement(`{"arbitrary":"whole-record replacement"}`) == nil {
		t.Fatal("untyped whole-record value admitted")
	}
}
