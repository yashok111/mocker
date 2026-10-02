package backendmodel

import (
	"encoding/json/v2"
	"strings"
)

// ArtifactApplyRPCFramingBytes reserves required tools/call fields and a full
// signed int64 request ID. Optional metadata/string IDs/padding remain subject
// to the unchanged global raw-body cap. SDK framing is independently tested.
const ArtifactApplyRPCFramingBytes int64 = 126

// reservedArtifactApplyBytes measures the canonical common REST/MCP envelope.
// Backslashes are admitted printable ASCII and require two encoded bytes each;
// using the serializer retains its exact escaping policy and the key's bound.
func reservedArtifactApplyBytes(pid string, in PreviewArtifactPinsInput) (int64, error) {
	type plainApply ApplyArtifactPinsInput
	envelope := struct {
		ProjectID string `json:"projectId"`
		plainApply
	}{ProjectID: pid, plainApply: plainApply{
		BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands,
		CandidateHash: strings.Repeat("0", 64), IdempotencyKey: strings.Repeat(`\`, MaxKeyLength),
	}}
	body, err := json.Marshal(envelope)
	return int64(len(body)), err
}

func (s *ArtifactService) admitApplyBody(pid string, in PreviewArtifactPinsInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if !ValidID(pid) {
		return notFound()
	}
	size, err := reservedArtifactApplyBytes(pid, in)
	if err != nil {
		return err
	}
	if size > s.maxApplyBodyBytes {
		details := map[string]any{"allowedBytes": s.maxApplyBodyBytes, "reservedApplyBytes": size}
		if s.globalMaxBodyBytes != nil {
			details["globalMaxBodyBytes"] = *s.globalMaxBodyBytes
			details["rpcFramingBytes"] = ArtifactApplyRPCFramingBytes
			details["reservedRPCBytes"] = size + ArtifactApplyRPCFramingBytes
		}
		return &FaultError{Status: 413, Code: "backend_artifact_apply_body_limit", Message: "Canonical Apply arguments and required RPC framing exceed the reserved artifact body limits", Details: details}
	}
	return nil
}
