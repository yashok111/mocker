package apidesign

import (
	"context"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/statediagram"
)

type StateDiagrams struct {
	DesignID   int64                  `json:"designId"`
	Version    int64                  `json:"version"`
	RevisionID int64                  `json:"revisionId"`
	Diagrams   []statediagram.Diagram `json:"diagrams"`
}

func (r *Repo) StateDiagrams(ctx context.Context, id int64) (StateDiagrams, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return StateDiagrams{}, err
	}
	root, err := decodeDocument(d.Draft.Document)
	if err != nil {
		return StateDiagrams{}, err
	}
	env, err := statediagram.Decode(root)
	if err != nil {
		return StateDiagrams{}, invalidField("/"+statediagram.Extension, err.Error())
	}
	return StateDiagrams{DesignID: id, Version: d.Design.Version, RevisionID: d.Draft.ID, Diagrams: env.Diagrams}, nil
}

// EditStateDiagram preserves every other contract field and fences the whole API
// version at Save. A concurrent API edit between Detail and Save returns 409.
func (r *Repo) EditStateDiagram(ctx context.Context, id, version int64, source, diagramID, action string, diagram *statediagram.Diagram, commands []statediagram.Command) (*Detail, error) {
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
	env, err := statediagram.Decode(root)
	if err != nil {
		return nil, invalidField("/"+statediagram.Extension, err.Error())
	}
	if (action == "create" || action == "save") && (diagram == nil || diagram.ID != diagramID) {
		return nil, invalidField("/diagram/id", "Нужна диаграмма с указанным id")
	}
	index := slices.IndexFunc(env.Diagrams, func(v statediagram.Diagram) bool { return v.ID == diagramID })
	if action != "create" && index < 0 {
		return nil, ErrNotFound
	}
	switch action {
	case "create":
		if index >= 0 {
			return nil, invalidField("/diagram/id", "Диаграмма с таким id уже существует")
		}
		env.Diagrams = append(env.Diagrams, *diagram)
	case "save":
		env.Diagrams[index] = *diagram
	case "delete":
		env.Diagrams = slices.Delete(env.Diagrams, index, index+1)
	case "commands":
		env.Diagrams[index], err = statediagram.ApplyCommands(env.Diagrams[index], commands)
		if err != nil {
			return nil, invalidField("/commands", err.Error())
		}
	default:
		return nil, fmt.Errorf("unknown diagram action %q", action)
	}
	root[statediagram.Extension] = env
	// Decode applies the envelope bounds too, before the general API validator.
	if _, err = statediagram.Decode(root); err != nil {
		return nil, invalidField("/"+statediagram.Extension, err.Error())
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: source, Summary: "State diagram: " + action + " " + diagramID})
}

type StateDiagramProposal struct {
	Diagram       *statediagram.Diagram `json:"diagram,omitempty"`
	Document      string                `json:"document,omitempty"`
	DataJSON      string                `json:"dataJSON,omitempty"`
	TransitionIDs []string              `json:"transitionIds,omitempty"`
}

func (r *Repo) ResolveStateDiagram(ctx context.Context, id int64, diagramID string, in StateDiagramProposal) (statediagram.Diagram, map[string]any, error) {
	d, err := r.Detail(ctx, id)
	if err != nil {
		return statediagram.Diagram{}, nil, err
	}
	raw := d.Draft.Document
	if in.Document != "" {
		raw = in.Document
	}
	if int64(len(raw)) > r.cfg.MaxBody {
		return statediagram.Diagram{}, nil, invalidField("/document", "Документ слишком большой")
	}
	root, err := decodeDocument(raw)
	if err != nil {
		return statediagram.Diagram{}, nil, invalidField("/document", err.Error())
	}
	if in.Diagram != nil {
		if in.Diagram.ID != diagramID {
			return statediagram.Diagram{}, nil, invalidField("/diagram/id", "ID диаграммы не совпадает с адресом")
		}
		return *in.Diagram, root, nil
	}
	env, err := statediagram.Decode(root)
	if err != nil {
		return statediagram.Diagram{}, nil, invalidField("/"+statediagram.Extension, err.Error())
	}
	for _, diagram := range env.Diagrams {
		if diagram.ID == diagramID {
			return diagram, root, nil
		}
	}
	return statediagram.Diagram{}, nil, ErrNotFound
}
