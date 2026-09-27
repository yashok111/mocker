package designscenario

import (
	"cmp"
	"fmt"
	"slices"
)

const maxFragmentDepth = 16

// ValidateFragments checks the versioned fragment model independently of API
// contracts and execution settings, so diagram exports can use the same rules.
func ValidateFragments(document Document) []Diagnostic {
	v := documentValidator{}
	positions := make(map[string]int, len(document.Messages))
	for i, message := range document.Messages {
		positions[message.ID] = i
	}
	if len(document.Fragments) > maxFragments {
		v.errorAt("/fragments", "contains too many fragments")
	}
	v.validateFragments(document.Fragments, positions, document.FormatVersion)
	if document.FormatVersion == 2 {
		v.validateFragmentTree(document.Fragments, positions)
	}
	return v.diagnostics
}

type fragmentRange struct{ start, end int }

func messageRange(from, to string, positions map[string]int) (fragmentRange, bool) {
	start, a := positions[from]
	end, b := positions[to]
	return fragmentRange{start, end}, a && b && start <= end
}

func (v *documentValidator) validateFragmentTree(items []Fragment, positions map[string]int) {
	byID := make(map[string]Fragment, len(items))
	for _, f := range items {
		byID[f.ID] = f
	}
	type scope struct{ parent, branch string }
	siblings := map[scope][]fragmentRange{}
	totalBranches := 0
	for i, f := range items {
		pointer := fmt.Sprintf("/fragments/%d", i)
		v.checkText(pointer+"/parentFragmentId", f.ParentFragmentID, true)
		v.checkText(pointer+"/parentBranchId", f.ParentBranchID, true)
		bounds, boundsOK := messageRange(f.FromMessageID, f.ToMessageID, positions)
		if boundsOK {
			key := scope{f.ParentFragmentID, f.ParentBranchID}
			siblings[key] = append(siblings[key], bounds)
		}
		totalBranches += len(f.Branches)
		v.validateBranches(f, pointer, positions)
		v.validateFragmentParent(f, pointer, byID, positions)
		v.validateFragmentAncestry(f, pointer, byID)
	}
	if totalBranches > 1000 {
		v.errorAt("/fragments", "total branch count exceeds 1000")
	}
	// Sort scope keys to keep diagnostics stable across validation and exports.
	keys := make([]scope, 0, len(siblings))
	for key := range siblings {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b scope) int { return cmp.Or(cmp.Compare(a.parent, b.parent), cmp.Compare(a.branch, b.branch)) })
	for _, key := range keys {
		ranges := siblings[key]
		slices.SortFunc(ranges, func(a, b fragmentRange) int { return cmp.Compare(a.start, b.start) })
		for i := 1; i < len(ranges); i++ {
			if ranges[i].start <= ranges[i-1].end {
				v.errorAt("/fragments", "sibling fragment ranges must not overlap")
				break
			}
		}
	}
}

func guardFragmentRemoval(document Document, removed map[string]struct{}) error {
	if document.FormatVersion != 2 {
		return nil
	}
	for { // Include response cascades before checking any boundaries.
		added := false
		for _, m := range document.Messages {
			if _, ok := removed[m.ReplyToID]; ok {
				if _, exists := removed[m.ID]; !exists {
					removed[m.ID] = struct{}{}
					added = true
				}
			}
		}
		if !added {
			break
		}
	}
	for _, f := range document.Fragments {
		ids := []string{f.FromMessageID, f.ToMessageID}
		for _, b := range f.Branches {
			ids = append(ids, b.FromMessageID, b.ToMessageID)
		}
		for _, id := range ids {
			if _, ok := removed[id]; ok {
				return invalidAt("/id", "message is a fragment or branch boundary; change boundaries or remove the fragment first")
			}
		}
	}
	return nil
}

func removeFragmentTree(items *[]Fragment, id string) bool {
	removed := map[string]bool{id: true}
	found := false
	for _, f := range *items {
		if f.ID == id {
			found = true
		}
	}
	if !found {
		return false
	}
	for {
		added := false
		for _, f := range *items {
			if removed[f.ParentFragmentID] && !removed[f.ID] {
				removed[f.ID] = true
				added = true
			}
		}
		if !added {
			break
		}
	}
	*items = slices.DeleteFunc(*items, func(f Fragment) bool { return removed[f.ID] })
	return true
}

func moveFragmentMessage(document *Document, id string, target int) error {
	candidate := *document
	candidate.Messages = slices.Clone(document.Messages)
	if !moveMessage(&candidate.Messages, id, target) {
		return invalidAt("", "message or target index does not exist")
	}
	if diagnostics := ValidateFragments(candidate); len(diagnostics) > 0 {
		return &InvalidError{Diagnostics: diagnostics}
	}
	before := make(map[string]int, len(document.Messages))
	after := make(map[string]int, len(candidate.Messages))
	for i, m := range document.Messages {
		before[m.ID] = i
	}
	for i, m := range candidate.Messages {
		after[m.ID] = i
	}
	// Every message keeps its membership in every frame and branch, including
	// messages swept over by a moved endpoint.
	sameRange := func(from, to string) bool {
		for id, position := range before {
			wasInside := position >= before[from] && position <= before[to]
			isInside := after[id] >= after[from] && after[id] <= after[to]
			if wasInside != isInside {
				return false
			}
		}
		return true
	}
	for _, f := range document.Fragments {
		if !sameRange(f.FromMessageID, f.ToMessageID) {
			return invalidAt("/index", "move changes fragment membership; change fragment boundaries first")
		}
		for _, b := range f.Branches {
			if !sameRange(b.FromMessageID, b.ToMessageID) {
				return invalidAt("/index", "move changes branch membership; change branch boundaries first")
			}
		}
	}
	document.Messages = candidate.Messages
	return nil
}

func (v *documentValidator) validateBranches(f Fragment, pointer string, positions map[string]int) {
	bounds, boundsOK := messageRange(f.FromMessageID, f.ToMessageID, positions)
	if f.Kind != "alt" && f.Branches != nil {
		v.errorAt(pointer+"/branches", "only alt fragments may have branches")
	}
	if f.Kind == "alt" {
		if len(f.Branches) < 2 || len(f.Branches) > 100 {
			v.errorAt(pointer+"/branches", "alt requires 2 to 100 branches")
		}
		next := bounds.start
		ids := map[string]bool{}
		for j, branch := range f.Branches {
			bp := fmt.Sprintf("%s/branches/%d", pointer, j)
			v.checkText(bp+"/id", branch.ID, false)
			v.checkText(bp+"/label", branch.Label, true)
			if ids[branch.ID] {
				v.errorAt(bp+"/id", "duplicate branch id")
			}
			ids[branch.ID] = true
			r, ok := messageRange(branch.FromMessageID, branch.ToMessageID, positions)
			if !ok {
				v.errorAt(bp, "branch boundaries must reference messages in order")
			}
			if ok && boundsOK && (r.start != next || r.end > bounds.end) {
				v.errorAt(bp, "branches must cover the fragment without gaps or overlaps")
			}
			next = r.end + 1
		}
		if boundsOK && next != bounds.end+1 {
			v.errorAt(pointer+"/branches", "branches must cover the entire fragment")
		}
	}
}

func (v *documentValidator) validateFragmentParent(f Fragment, pointer string, byID map[string]Fragment, positions map[string]int) {
	bounds, boundsOK := messageRange(f.FromMessageID, f.ToMessageID, positions)
	if f.ParentFragmentID == "" {
		if f.ParentBranchID != "" {
			v.errorAt(pointer+"/parentBranchId", "branch requires a parent fragment")
		}
	} else if parent, ok := byID[f.ParentFragmentID]; !ok {
		v.errorAt(pointer+"/parentFragmentId", "parent fragment does not exist")
	} else {
		outer, ok := messageRange(parent.FromMessageID, parent.ToMessageID, positions)
		if parent.Kind == "alt" {
			branch := slices.IndexFunc(parent.Branches, func(b FragmentBranch) bool { return b.ID == f.ParentBranchID })
			if f.ParentBranchID == "" || branch < 0 {
				v.errorAt(pointer+"/parentBranchId", "parent alt branch does not exist")
				ok = false
			} else {
				b := parent.Branches[branch]
				outer, ok = messageRange(b.FromMessageID, b.ToMessageID, positions)
			}
		} else if f.ParentBranchID != "" {
			v.errorAt(pointer+"/parentBranchId", "only an alt parent accepts a branch")
		}
		if ok && boundsOK && (bounds.start < outer.start || bounds.end > outer.end) {
			v.errorAt(pointer, "child must be contained in its parent range or branch")
		}
	}
}

func (v *documentValidator) validateFragmentAncestry(f Fragment, pointer string, byID map[string]Fragment) {
	seen := map[string]bool{}
	current := f
	for depth := 1; ; depth++ {
		if seen[current.ID] {
			v.errorAt(pointer+"/parentFragmentId", "fragment parent cycle")
			break
		}
		seen[current.ID] = true
		if depth > maxFragmentDepth {
			v.errorAt(pointer+"/parentFragmentId", "fragment depth exceeds 16")
			break
		}
		if current.ParentFragmentID == "" {
			break
		}
		parent, ok := byID[current.ParentFragmentID]
		if !ok {
			break
		}
		current = parent
	}
}
