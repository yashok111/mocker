package apidesign

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/statediagram"
)

type StateDiagramExecutionState struct {
	DiagramID      string                     `json:"diagramId"`
	Name           string                     `json:"name"`
	Entity         statediagram.EntityBinding `json:"entity"`
	State          string                     `json:"state"`
	OperationCount int                        `json:"operationCount"`
}

type StateDiagramExecution struct {
	DesignID   int64                        `json:"designId"`
	Version    int64                        `json:"version"`
	RevisionID int64                        `json:"revisionId"`
	Diagrams   []StateDiagramExecutionState `json:"diagrams"`
}

// StateDiagramExecution compares complete saved copies from one immutable
// revision. Layout and labels count too, since applying copies the whole source.
func (r *Repo) StateDiagramExecution(ctx context.Context, id int64) (StateDiagramExecution, error) {
	design, draft, err := r.responseRuleSnapshot(ctx, id)
	if err != nil {
		return StateDiagramExecution{}, err
	}
	root, authoring, err := decodeStateExecutionDocument(draft.Document)
	if err != nil {
		return StateDiagramExecution{}, err
	}
	execution, err := statediagram.DecodeExecution(root)
	if err != nil {
		return StateDiagramExecution{}, stateDiagramExecutionError(err)
	}
	programs, err := statediagram.CompileExecution(ctx, root)
	if err != nil {
		return StateDiagramExecution{}, stateDiagramExecutionError(err)
	}
	result := StateDiagramExecution{DesignID: id, Version: design.Version, RevisionID: draft.ID, Diagrams: []StateDiagramExecutionState{}}
	for _, copy := range execution.Diagrams {
		if copy.Entity == nil {
			return StateDiagramExecution{}, invalidField("/"+statediagram.ExecutionExtension, "У применённой диаграммы отсутствует сущность")
		}
		state := "missing"
		if i := slices.IndexFunc(authoring.Diagrams, func(diagram statediagram.Diagram) bool { return diagram.ID == copy.ID }); i >= 0 {
			state = "outdated"
			if reflect.DeepEqual(authoring.Diagrams[i], copy) {
				state = "current"
			}
		}
		count := 0
		for _, program := range programs {
			if program.ID() == copy.ID {
				count++
			}
		}
		result.Diagrams = append(result.Diagrams, StateDiagramExecutionState{DiagramID: copy.ID, Name: copy.Name, Entity: *copy.Entity, State: state, OperationCount: count})
	}
	return result, nil
}

// EditStateDiagramExecution fences the whole API and selects the saved source
// under one writer. SaveTx handles revision history and frozen publication just
// like an ordinary API edit, while entity datasets remain workspace-owned.
func (r *Repo) EditStateDiagramExecution(ctx context.Context, id, version int64, source, diagramID string, apply bool) (*Detail, error) {
	if version <= 0 {
		return nil, invalidField("/expectedVersion", "Обязательна положительная версия API")
	}
	if !statediagram.ValidID(diagramID) {
		return nil, invalidField("/diagramId", "Некорректный ID диаграммы")
	}
	if err := checkSource(source); err != nil {
		return nil, err
	}
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		design, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := checkVersion(design, version); err != nil {
			return err
		}
		draft, err := getRevision(ctx, tx, id, design.DraftRevisionID)
		if err != nil {
			return err
		}
		root, authoring, err := decodeStateExecutionDocument(draft.Document)
		if err != nil {
			return err
		}
		execution, err := statediagram.DecodeExecution(root)
		if err != nil {
			return stateDiagramExecutionError(err)
		}
		i := slices.IndexFunc(execution.Diagrams, func(diagram statediagram.Diagram) bool { return diagram.ID == diagramID })
		summary := "Remove state diagram: " + diagramID
		if apply {
			selected := slices.IndexFunc(authoring.Diagrams, func(diagram statediagram.Diagram) bool { return diagram.ID == diagramID })
			if selected < 0 {
				return ErrNotFound
			}
			unchanged := i >= 0 && reflect.DeepEqual(execution.Diagrams[i], authoring.Diagrams[selected])
			if i < 0 {
				execution.Diagrams = append(execution.Diagrams, authoring.Diagrams[selected])
			} else {
				execution.Diagrams[i] = authoring.Diagrams[selected]
			}
			root[statediagram.ExecutionExtension] = execution
			// Validate the candidate even for an identical-copy apply. This also
			// checks conflicts against every other applied response source.
			if _, err := statediagram.CompileExecution(ctx, root); err != nil {
				return stateDiagramExecutionError(err)
			}
			if unchanged {
				result, err = detailTx(ctx, tx, id)
				return err
			}
			summary = "Apply state diagram: " + diagramID
		} else {
			if i < 0 {
				result, err = detailTx(ctx, tx, id)
				return err
			}
			execution.Diagrams = slices.Delete(execution.Diagrams, i, i+1)
			root[statediagram.ExecutionExtension] = execution
		}
		raw, err := jsonx.Marshal(root)
		if err != nil {
			return err
		}
		result, err = r.SaveTx(ctx, tx, id, SaveInput{ExpectedVersion: version, Document: string(raw), Source: source, Summary: summary})
		return err
	})
	return result, err
}

func decodeStateExecutionDocument(document string) (map[string]any, statediagram.Envelope, error) {
	root, err := decodeDocument(document)
	if err != nil {
		return nil, statediagram.Envelope{}, invalidField("/document", err.Error())
	}
	authoring, err := statediagram.Decode(root)
	if err != nil {
		return nil, statediagram.Envelope{}, invalidField("/"+statediagram.Extension, err.Error())
	}
	return root, authoring, nil
}

func stateDiagramExecutionError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if field, ok := errors.AsType[*statediagram.FieldError](err); ok {
		return invalidField(field.Pointer, field.Message)
	}
	return invalidField("/"+statediagram.ExecutionExtension, err.Error())
}
