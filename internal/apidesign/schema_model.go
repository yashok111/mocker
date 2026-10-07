package apidesign

import (
	"context"
	"errors"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/schemamodel"
	"github.com/yashok111/mocker/internal/specs"
)

type SchemaModelDetail struct {
	DesignID   int64             `json:"designId"`
	Version    int64             `json:"version"`
	RevisionID int64             `json:"revisionId"`
	Model      schemamodel.Model `json:"model"`
}
type SchemaModelProposal struct {
	Document *string               `json:"document,omitempty"`
	Commands []schemamodel.Command `json:"commands,omitempty"`
}
type SchemaModelPreview struct {
	Document    string            `json:"document"`
	Model       schemamodel.Model `json:"model"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
	Valid       bool              `json:"valid"`
}

func schemaModelError(err error) error {
	if problem, ok := errors.AsType[*schemamodel.Error](err); ok {
		return invalidField(problem.Pointer, err.Error())
	}
	return err
}
func (r *Repo) SchemaModel(ctx context.Context, id int64) (SchemaModelDetail, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return SchemaModelDetail{}, err
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		return SchemaModelDetail{}, err
	}
	model, err := schemamodel.Project(root)
	if err != nil {
		return SchemaModelDetail{}, schemaModelError(err)
	}
	return SchemaModelDetail{DesignID: id, Version: d.Design.Version, RevisionID: d.Draft.ID, Model: model}, nil
}

// PreviewSchemaModel never creates revisions or updates mock workspaces.
func (r *Repo) PreviewSchemaModel(ctx context.Context, id int64, in SchemaModelProposal) (SchemaModelPreview, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return SchemaModelPreview{}, err
	}
	raw := d.Draft.Document
	if in.Document != nil {
		raw = *in.Document
	}
	if int64(len(raw)) > r.cfg.MaxBody {
		return SchemaModelPreview{}, specs.ErrTooLarge
	}
	root, err := decodeDocument(raw)
	if err != nil {
		return SchemaModelPreview{}, invalidField("/document", err.Error())
	}
	root, err = schemamodel.Apply(root, in.Commands)
	if err != nil {
		return SchemaModelPreview{}, schemaModelError(err)
	}
	model, err := schemamodel.Project(root)
	if err != nil {
		return SchemaModelPreview{}, schemaModelError(err)
	}
	encoded, err := jsonx.MarshalIndent(root, "", "  ")
	if err != nil {
		return SchemaModelPreview{}, err
	}
	diagnostics, err := r.validateContext(ctx, string(encoded))
	if err != nil {
		return SchemaModelPreview{}, err
	}
	return SchemaModelPreview{Document: string(encoded), Model: model, Diagnostics: diagnostics, Valid: len(diagnostics) == 0}, nil
}

// ApplySchemaModelCommands reads a draft snapshot, then Save atomically checks
// the same version before writing the transformed document and mock projection.
func (r *Repo) ApplySchemaModelCommands(ctx context.Context, id, version int64, source string, commands []schemamodel.Command) (*Detail, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = checkVersion(d.Design, version); err != nil {
		return nil, err
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		return nil, err
	}
	root, err = schemamodel.Apply(root, commands)
	if err != nil {
		return nil, schemaModelError(err)
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: source, Summary: "Schema model commands"})
}
