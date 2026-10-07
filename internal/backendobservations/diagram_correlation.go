package backendobservations

import (
	"reflect"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func selectorKey(s bm.DiagramScopeSelector) string { b, _ := canonical(s); return string(b) }
func mapDiagramRow(row CorrelationRow, compatible bool, scope *bm.DiagramScope, members map[string][]bm.DiagramRef, protected bool) DiagramCorrelationRow {
	out := DiagramCorrelationRow{RecordID: row.RecordID, Selectors: []bm.DiagramScopeSelector{}, Basis: "unresolved", Gaps: []string{}}
	if !compatible || row.Selected == nil || len(row.Candidates) != 1 {
		out.Gaps = append(out.Gaps, "Source mapping is absent, ambiguous or build-incompatible")
		return out
	}
	if protected {
		out.Gaps = append(out.Gaps, "Endpoint execution does not prove state transition; explicit state-change witness required")
		return out
	}
	for _, sel := range scope.Selectors {
		refs, ok := members[selectorKey(sel)]
		if !ok {
			refs = members[sel.ID]
		}
		for _, ref := range refs {
			if reflect.DeepEqual(ref, *row.Selected) {
				out.Selectors = append(out.Selectors, sel)
				break
			}
		}
	}
	if len(out.Selectors) > 1 {
		out.Selectors = []bm.DiagramScopeSelector{}
		out.Gaps = append(out.Gaps, "Ambiguous diagram membership; select an explicit instrumented identity")
		return out
	}
	if len(out.Selectors) == 1 {
		out.Basis = "source_mapping"
	} else {
		out.Gaps = append(out.Gaps, "No explicit diagram membership for the exact source identity")
	}
	return out
}
