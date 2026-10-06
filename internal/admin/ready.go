package admin

// Ready reports, by the name of the setter that would fix it, every source
// a PRODUCTION Server must have and this one does not. An empty result
// means fully wired.
//
// The reasoning is [mockplane.Plane.Ready]'s word for word (see that
// method's own doc comment, internal/mockplane/ready.go): each setter here
// is deliberately forgiving — a Server that never got one still registers
// its routes and answers 503 service_unavailable rather than panicking on a
// nil interface, precisely so a test harness can wire only what it
// exercises. That is what lets a green `go test` hide a feature that is
// dead in production, twice now in this project's history, since only
// cmd/mocker/main.go can forget a setter and no test in this package ever
// calls it.
//
// The repositories and rate limiter are non-nil by construction. The required
// APIArtifactService is additionally checked because its transport depends on
// both backend and API owners; a missing service must prevent startup.
//
// REQUIRED vs OPTIONAL, decided from the setters' own doc comments:
//
//   - SetLiveState, SetTraffic, SetPreviewer, SetScenarioExecutor — required. Each takes an
//     instance the MOCK plane holds too (the same *livestate.Store, the
//     same *traffic.Recorder, the same *mockplane.Plane), there is no
//     configuration that turns any of them off, and a deployment missing
//     one answers 503 on a route the UI shows a button for.
//   - SetStream — required, and required in BOTH of its arguments. Its own
//     doc comment allows a nil mock registry for "a test harness with no
//     mock plane", which cmd/mocker never is: a nil mock leaves the `mock`
//     object silently out of GET /api/stream/stats instead of refusing
//     anything, the quietest possible form of the failure this method
//     exists to catch. Both are therefore reported under the one setter
//     name, since one call supplies both.
//   - SetMCP — OPTIONAL, and the only optional source on this type.
//     MOCKER_MCP_KEY unset means /mcp is not a route at all (the "no
//     surface" state the MCP slice requires), so a nil handler here is a
//     deployment's deliberate choice rather than a forgotten line. Nothing
//     below checks it; cmd/mocker's own conditional is where that decision
//     lives.
//
// The names come back in the order this file checks them, so an error built
// from the slice reads as a to-do list rather than a set.
func (s *Server) Ready() []string {
	var missing []string
	if s.backendObservations == nil {
		missing = append(missing, "SetBackendObservations")
	}
	if s.backendReplay == nil {
		missing = append(missing, "SetBackendReplay")
	}
	if s.backendAnalysis == nil || s.backendAnalysisRepo == nil {
		missing = append(missing, "SetBackendAnalysis")
	}
	// Artifact pins require both owners. Keep this explicit even though New
	// constructs the service, so incomplete production wiring fails startup.
	if s.backendAPIArtifacts == nil {
		missing = append(missing, "NewAPIArtifactService")
	}
	if s.backendArtifacts == nil {
		missing = append(missing, "NewArtifactService")
	}
	if s.scenarioArtifactSnapshots == nil {
		missing = append(missing, "ScenarioArtifactSnapshots")
	}
	if s.liveState == nil {
		missing = append(missing, "SetLiveState")
	}
	if s.traffic == nil {
		missing = append(missing, "SetTraffic")
	}
	if s.streamReg == nil || s.mockStreams == nil {
		missing = append(missing, "SetStream")
	}
	if s.previewer == nil {
		missing = append(missing, "SetPreviewer")
	}
	if s.scenarioExecutor == nil {
		missing = append(missing, "SetScenarioExecutor")
	}
	return missing
}
