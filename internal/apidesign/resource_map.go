package apidesign

import (
	"context"
	"errors"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resourcemap"
	"github.com/yashok111/mocker/internal/specs"
)

type ResourceMapDetail struct {
	DesignID   int64             `json:"designId"`
	Version    int64             `json:"version"`
	RevisionID int64             `json:"revisionId"`
	Model      resourcemap.Model `json:"model"`
}
type ResourceMapProposal struct {
	Document *string               `json:"document,omitempty"`
	Commands []resourcemap.Command `json:"commands,omitempty"`
}
type ResourceMapPreview struct {
	Document    string            `json:"document"`
	Model       resourcemap.Model `json:"model"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
	Valid       bool              `json:"valid"`
}

func resourceMapError(err error) error {
	if problem, ok := errors.AsType[*resourcemap.Error](err); ok {
		return invalidField(problem.Pointer, problem.Message)
	}
	return err
}

func (r *Repo) ResourceMap(ctx context.Context, id int64) (ResourceMapDetail, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return ResourceMapDetail{}, err
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		return ResourceMapDetail{}, err
	}
	model, err := resourcemap.Project(root)
	if err != nil {
		return ResourceMapDetail{}, resourceMapError(err)
	}
	return ResourceMapDetail{DesignID: id, Version: d.Design.Version, RevisionID: d.Draft.ID, Model: model}, nil
}

// PreviewResourceMap transforms a normalized draft without writing revisions.
func (r *Repo) PreviewResourceMap(ctx context.Context, id int64, in ResourceMapProposal) (ResourceMapPreview, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return ResourceMapPreview{}, err
	}
	raw := d.Draft.Document
	if in.Document != nil {
		raw = *in.Document
	}
	if int64(len(raw)) > r.cfg.MaxBody {
		return ResourceMapPreview{}, specs.ErrTooLarge
	}
	keyed, err := withOperationKeys(raw, d.Draft.Document, 0)
	if err != nil {
		return ResourceMapPreview{}, err
	}
	root, err := decodeDocument(keyed)
	if err != nil {
		return ResourceMapPreview{}, invalidField("/document", err.Error())
	}
	root, err = resourcemap.Apply(root, in.Commands)
	if err != nil {
		return ResourceMapPreview{}, resourceMapError(err)
	}
	model, err := resourcemap.Project(root)
	if err != nil {
		return ResourceMapPreview{}, resourceMapError(err)
	}
	encoded, err := jsonx.MarshalIndent(root, "", "  ")
	if err != nil {
		return ResourceMapPreview{}, err
	}
	if int64(len(encoded)) > r.cfg.MaxBody {
		return ResourceMapPreview{}, specs.ErrTooLarge
	}
	diagnostics, err := r.Validate(ctx, string(encoded))
	if err != nil {
		return ResourceMapPreview{}, err
	}
	return ResourceMapPreview{Document: string(encoded), Model: model, Diagnostics: diagnostics, Valid: len(diagnostics) == 0}, nil
}

// ApplyResourceMapCommands uses Save's version fence and revision transaction.
func (r *Repo) ApplyResourceMapCommands(ctx context.Context, id, version int64, source string, commands []resourcemap.Command) (*Detail, error) {
	if len(commands) == 0 {
		return nil, invalidField("/commands", "Укажите хотя бы одну команду")
	}
	d, err := r.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkVersion(d.Design, version); err != nil {
		return nil, err
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		return nil, err
	}
	root, err = resourcemap.Apply(root, commands)
	if err != nil {
		return nil, resourceMapError(err)
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: source, Summary: "Resource map commands"})
}
