package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
)

// A start view is an immutable view version, never an alias to the latest view.
type ProjectStartView struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

func (v *ProjectStartView) UnmarshalJSON(raw []byte) error {
	type wire ProjectStartView
	if err := strictAPIObject(raw, []string{"kind", "id", "version"}, nil, (*wire)(v)); err != nil {
		return err
	}
	return v.Validate()
}

func (v ProjectStartView) Validate() error {
	if (v.Kind != "saved_view" && v.Kind != "diagram_view") || !ValidID(v.ID) || v.Version <= 0 {
		return invalid("startView", "Select an exact saved_view or diagram_view UUID and positive version")
	}
	return nil
}

func validateProjectStartView(ctx context.Context, q importReader, pid string, v *ProjectStartView) error {
	if v == nil {
		return nil
	}
	if err := v.Validate(); err != nil {
		return err
	}
	if v.Kind == "diagram_view" {
		_, err := loadDiagramView(ctx, q, pid, v.ID, v.Version)
		return err
	}
	_, err := loadSavedView(ctx, q, pid, v.ID, v.Version)
	return err
}

func writeProjectStartView(ctx context.Context, tx *sql.Tx, p *Project) error {
	if err := validateProjectStartView(ctx, tx, p.ID, p.StartView); err != nil {
		return err
	}
	var value any
	if p.StartView != nil {
		raw, err := json.Marshal(p.StartView)
		if err != nil {
			return err
		}
		value = string(raw)
	}
	_, err := tx.ExecContext(ctx, `UPDATE backend_projects SET start_view_json=? WHERE id=?`, value, p.ID)
	return err
}

func validateStartViewCommand(c Command) error {
	if c.Name != "" || c.AnnotationID != "" || c.Target != nil || c.Body != "" {
		return invalid("commands", "Start-view commands cannot contain annotation or rename fields")
	}
	if c.Type == "clear_start_view" {
		if c.StartView != nil {
			return invalid("commands", "clear_start_view has no payload")
		}
		return nil
	}
	if c.StartView == nil {
		return invalid("startView", "An exact view pin is required")
	}
	return c.StartView.Validate()
}
