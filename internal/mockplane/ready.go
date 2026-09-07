package mockplane

// Ready reports, by the name of the call that would fix it, every source a
// PRODUCTION Plane must have and this one does not. An empty result means
// fully wired.
//
// Why this exists at all. Every setter on this type is deliberately
// forgiving: a nil interface field compiles, and each setter's own doc
// comment promises a graceful degradation rather than a panic — a 503 on
// the reserved routes, a 404 on the asset route, or (worse, because it is
// silent) a layer that is simply never composed and a request answered as
// if the feature had never been built. That contract is what keeps every
// test in this package free to wire only the one source it exercises, and
// it is also exactly the shape CLAUDE.md warns about: this project twice
// shipped a fully green `go test` with a feature dead in production,
// because the tests wire their dependency explicitly and only
// cmd/mocker/main.go can forget to. Nothing in the type system can catch
// that — a constructor with options would not have caught either incident,
// since a nil interface handed to a constructor compiles just as happily
// as a setter never called. So the check is a runtime one, run once at
// startup, before the listener opens.
//
// REQUIRED here means "cmd/mocker wires it unconditionally, for every
// deployment, under every configuration". Every source on this struct
// qualifies, and there is no OPTIONAL one today:
//
//   - the eight domain sources (overrides, live state, traffic, custom
//     endpoints, scenarios, resources, entities, assets) have no
//     configuration switch at all — a deployment either serves them or
//     silently does not;
//   - SetStreams is required even when MOCKER_STREAM_MAX_CONNS is 0,
//     because a zero cap refuses streaming INSIDE an already-wired
//     registry ([stream.NewWorkspaceRegistry]'s own comment) rather than
//     leaving one unwired — "streaming off" is a registry that says no,
//     not a nil field;
//   - the one configuration switch that removes a feature outright,
//     MOCKER_MCP_KEY, belongs to the admin plane, so its optionality is
//     documented in that package's own Ready (internal/admin/ready.go).
//
// The two [New] parameters are reported under the parameter's name rather
// than a setter's, because that is the call a reader has to go fix: New's
// own doc comment allows a nil specs ("no spec support"), which is true of
// a test harness and never true of cmd/mocker.
//
// The names come back in the order this file checks them, so an error
// built from the slice reads as a to-do list rather than a set.
func (p *Plane) Ready() []string {
	var missing []string
	if p.src == nil {
		missing = append(missing, "New(src)")
	}
	if p.specs == nil {
		missing = append(missing, "New(specs)")
	}
	if p.overrides == nil {
		missing = append(missing, "SetOverrides")
	}
	if p.livestate == nil {
		missing = append(missing, "SetLiveState")
	}
	if p.traffic == nil {
		missing = append(missing, "SetTraffic")
	}
	if p.custom == nil {
		missing = append(missing, "SetCustomEndpoints")
	}
	if p.scenarios == nil {
		missing = append(missing, "SetScenarios")
	}
	if p.resources == nil {
		missing = append(missing, "SetResources")
	}
	if p.entities == nil {
		missing = append(missing, "SetEntities")
	}
	if p.assets == nil {
		missing = append(missing, "SetAssets")
	}
	if p.streams == nil {
		missing = append(missing, "SetStreams")
	}
	return missing
}
