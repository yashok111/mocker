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
// Everything New builds for itself (the repositories, the rate limiter) is
// non-nil by construction and is not checked here: this method is about the
// wiring cmd/mocker owns, not about New's own arguments.
//
// REQUIRED vs OPTIONAL, decided from the setters' own doc comments:
//
//   - SetLiveState, SetTraffic, SetPreviewer — required. Each takes an
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
	return missing
}
