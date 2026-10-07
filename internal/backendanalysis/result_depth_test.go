package backendanalysis

import "testing"

// TestResultDepthZeroFilters pins review 2026-10-06, F25: the route, OpenAPI
// and the MCP schema accept depth=0, but the query carried a plain int that
// the filter read as "unset", so an explicit depth=0 returned records of
// every depth while the agent believed the page was scoped.
func TestResultDepthZeroFilters(t *testing.T) {
	direct, deep := []byte(`{"depth":0}`), []byte(`{"depth":3}`)
	if !matches(direct, ResultQuery{DepthSet: true}) || matches(deep, ResultQuery{DepthSet: true}) {
		t.Error("explicit depth=0 must keep depth 0 and drop deeper records")
	}
	if !matches(deep, ResultQuery{}) {
		t.Error("an absent depth must not filter")
	}
	if !matches(deep, ResultQuery{Depth: 3, DepthSet: true}) || matches(deep, ResultQuery{Depth: 2, DepthSet: true}) {
		t.Error("depth N keeps records at most N deep")
	}
}
