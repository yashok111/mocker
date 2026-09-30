package mockplane

import (
	"context"

	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/statediagram"
	"github.com/yashok111/mocker/internal/workspaces"
)

// Managed designs retain dormant rows. Only applied entity targets and their
// ancestors enter the runtime roster; ordinary workspaces keep all resources.
func (p *Plane) executionResources(ctx context.Context, ws *workspaces.Workspace, rows map[string]*resources.Resource, rules map[string]*responserules.Program, states map[string]*statediagram.Program) (map[string]*resources.Resource, error) {
	source, ok := p.src.(managedWorkspaceSource)
	if !ok {
		return rows, nil
	}
	designID, err := source.ManagedDesign(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	if designID == 0 {
		return rows, nil
	}
	active := map[string]*resources.Resource{}
	include := func(target string) {
		for family := target; family != ""; family = router.ParentFamily(family) {
			if row := rows[family]; row != nil {
				active[family] = row
			}
		}
	}
	for _, program := range rules {
		for _, family := range program.EntityFamilies() {
			include(family)
		}
	}
	for _, program := range states {
		include(program.Entity().Family)
	}
	return active, nil
}
