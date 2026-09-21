package designscenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (r *Repo) Apply(ctx context.Context, id int64, input CommandsInput) (*Detail, error) {
	if err := checkSource(input.Source); err != nil {
		return nil, err
	}
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		scenario, err := getScenario(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(scenario, input.ExpectedVersion); err != nil {
			return err
		}
		current, err := getRevision(ctx, tx, id, scenario.DraftRevisionID)
		if err != nil {
			return err
		}
		document, err := cloneDocument(current.Document)
		if err != nil {
			return err
		}
		for index, command := range input.Commands {
			if err = r.applyCommand(ctx, tx, &document, command, input.Source, input.OwnerID); err != nil {
				if invalid, ok := errors.AsType[*InvalidError](err); ok {
					for diagnosticIndex := range invalid.Diagnostics {
						invalid.Diagnostics[diagnosticIndex].Pointer = fmt.Sprintf("/commands/%d", index) + invalid.Diagnostics[diagnosticIndex].Pointer
					}
				}
				return err
			}
		}
		result, err = r.saveTx(ctx, tx, id, input.ExpectedVersion, document, current.FormDrafts, input.Summary, input.Source)
		return err
	})
	return result, err
}

// ApplyCommands is a descriptive alias retained for callers that expose the
// operation under its full transport name.
func (r *Repo) ApplyCommands(ctx context.Context, id int64, input CommandsInput) (*Detail, error) {
	return r.Apply(ctx, id, input)
}

func (r *Repo) applyCommand(ctx context.Context, tx *sql.Tx, document *Document, command Command, source string, ownerID *int64) error {
	switch command.Type {
	case "set_title":
		document.Title = command.Title
	case "upsert_participant":
		if command.Participant == nil {
			return invalidAt("/participant", "participant is required")
		}
		upsertParticipant(&document.Participants, *command.Participant)
	case "remove_participant":
		if !removeParticipant(document, command.ID) {
			return invalidAt("/id", "participant does not exist")
		}
	case "move_participant":
		if command.Index == nil {
			return invalidAt("/index", "index is required")
		}
		if !moveParticipant(&document.Participants, command.ID, *command.Index) {
			return invalidAt("", "participant or target index does not exist")
		}
	case "upsert_message":
		if command.Message == nil {
			return invalidAt("/message", "message is required")
		}
		upsertMessage(&document.Messages, *command.Message)
	case "remove_message":
		if !removeMessages(document, map[string]struct{}{command.ID: {}}) {
			return invalidAt("/id", "message does not exist")
		}
	case "move_message":
		if command.Index == nil {
			return invalidAt("/index", "index is required")
		}
		if !moveMessage(&document.Messages, command.ID, *command.Index) {
			return invalidAt("", "message or target index does not exist")
		}
	case "upsert_fragment":
		if command.Fragment == nil {
			return invalidAt("/fragment", "fragment is required")
		}
		upsertFragment(&document.Fragments, *command.Fragment)
	case "remove_fragment":
		if !removeFragment(&document.Fragments, command.ID) {
			return invalidAt("/id", "fragment does not exist")
		}
	case "bind_operation":
		message := findMessage(document.Messages, command.MessageID)
		if message == nil {
			return invalidAt("/messageId", "message does not exist")
		}
		if findContract(document.Contracts, command.ContractID) == nil {
			return invalidAt("/contractId", "contract does not exist")
		}
		message.Operation = &OperationBinding{ContractID: command.ContractID, OperationKey: command.OperationKey}
	case "create_operation":
		return createOperation(document, command)
	case "create_contract":
		return createContract(document, command.Contract)
	case "import_contract":
		return r.importContract(ctx, tx, document, command)
	case "refresh_contract":
		return r.refreshContract(ctx, tx, document, command)
	case "detach_contract":
		contract := findContract(document.Contracts, command.ContractID)
		if contract == nil {
			return invalidAt("/contractId", "contract does not exist")
		}
		contract.Mode = "copy"
	case "materialize_contract":
		return r.materializeContract(ctx, tx, document, command, source, ownerID)
	default:
		return invalidAt("/type", "unknown command")
	}
	return nil
}

func createContract(document *Document, contract *Contract) error {
	if contract == nil {
		return invalidAt("/contract", "contract is required")
	}
	if strings.TrimSpace(contract.ID) == "" {
		return invalidAt("/contract/id", "contract id must not be empty")
	}
	if findContract(document.Contracts, contract.ID) != nil {
		return invalidAt("/contract/id", "contract id already exists")
	}
	if contract.Mode != "" && contract.Mode != "copy" {
		return invalidAt("/contract/mode", "new contract must be an independent copy")
	}
	if contract.Source != nil {
		return invalidAt("/contract/source", "new contract must not have an API source")
	}
	if !isJSONObject(contract.Document) {
		return invalidAt("/contract/document", "API document must be a JSON object")
	}
	document.Contracts = append(document.Contracts, *contract)
	return nil
}

func upsertParticipant(items *[]Participant, value Participant) {
	for index := range *items {
		if (*items)[index].ID == value.ID {
			(*items)[index] = value
			return
		}
	}
	*items = append(*items, value)
}

func upsertMessage(items *[]Message, value Message) {
	for index := range *items {
		if (*items)[index].ID == value.ID {
			(*items)[index] = value
			return
		}
	}
	*items = append(*items, value)
}

func upsertFragment(items *[]Fragment, value Fragment) {
	for index := range *items {
		if (*items)[index].ID == value.ID {
			(*items)[index] = value
			return
		}
	}
	*items = append(*items, value)
}

func removeParticipant(document *Document, id string) bool {
	index := slices.IndexFunc(document.Participants, func(participant Participant) bool { return participant.ID == id })
	if index < 0 {
		return false
	}
	document.Participants = slices.Delete(document.Participants, index, index+1)
	removed := map[string]struct{}{}
	for _, message := range document.Messages {
		if message.FromID == id || message.ToID == id {
			removed[message.ID] = struct{}{}
		}
	}
	removeMessages(document, removed)
	return true
}

func removeMessages(document *Document, removed map[string]struct{}) bool {
	if len(removed) == 0 {
		return false
	}
	found := false
	for {
		added := false
		for _, message := range document.Messages {
			if _, exists := removed[message.ID]; exists {
				found = true
				continue
			}
			if _, exists := removed[message.ReplyToID]; exists {
				removed[message.ID] = struct{}{}
				added = true
			}
		}
		if !added {
			break
		}
	}
	document.Messages = slices.DeleteFunc(document.Messages, func(message Message) bool {
		_, remove := removed[message.ID]
		return remove
	})
	document.Fragments = slices.DeleteFunc(document.Fragments, func(fragment Fragment) bool {
		_, removeFrom := removed[fragment.FromMessageID]
		_, removeTo := removed[fragment.ToMessageID]
		return removeFrom || removeTo
	})
	return found
}

func moveParticipant(items *[]Participant, id string, target int) bool {
	index := slices.IndexFunc(*items, func(item Participant) bool { return item.ID == id })
	if index < 0 || target < 0 || target >= len(*items) {
		return false
	}
	value := (*items)[index]
	*items = slices.Delete(*items, index, index+1)
	*items = slices.Insert(*items, target, value)
	return true
}

func moveMessage(items *[]Message, id string, target int) bool {
	index := slices.IndexFunc(*items, func(item Message) bool { return item.ID == id })
	if index < 0 || target < 0 || target >= len(*items) {
		return false
	}
	value := (*items)[index]
	*items = slices.Delete(*items, index, index+1)
	*items = slices.Insert(*items, target, value)
	return true
}

func removeFragment(items *[]Fragment, id string) bool {
	index := slices.IndexFunc(*items, func(item Fragment) bool { return item.ID == id })
	if index < 0 {
		return false
	}
	*items = slices.Delete(*items, index, index+1)
	return true
}

func findMessage(items []Message, id string) *Message {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func findContract(items []Contract, id string) *Contract {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func createOperation(document *Document, command Command) error {
	message := findMessage(document.Messages, command.MessageID)
	if message == nil {
		return invalidAt("/messageId", "message does not exist")
	}
	method := strings.ToLower(command.Method)
	if !slices.Contains(operationMethods, method) {
		return invalidAt("/method", "unsupported HTTP method")
	}
	if !strings.HasPrefix(command.Path, "/") {
		return invalidAt("/path", "path must start with /")
	}
	contractID := command.ContractID
	contract := findContract(document.Contracts, contractID)
	if contract == nil && contractID != "" {
		return invalidAt("/contractId", "contract does not exist")
	}
	if contract == nil {
		contractID = uuid.New().String()
		contract = &Contract{
			ID:       contractID,
			Name:     "Новый API",
			Mode:     "copy",
			Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"Новый API","version":"1"},"paths":{}}`),
		}
		document.Contracts = append(document.Contracts, *contract)
		contract = &document.Contracts[len(document.Contracts)-1]
	}
	value, err := decodeJSONValue(contract.Document)
	if err != nil {
		return invalidAt("/contractId", "contract document is invalid")
	}
	api, ok := value.(map[string]any)
	if !ok {
		return invalidAt("/contractId", "contract document is invalid")
	}
	paths, _ := api["paths"].(map[string]any)
	if paths == nil {
		paths = map[string]any{}
		api["paths"] = paths
	}
	pathItem, _ := paths[command.Path].(map[string]any)
	if pathItem == nil {
		pathItem = map[string]any{}
		paths[command.Path] = pathItem
	}
	if _, exists := pathItem[method]; exists {
		return invalidAt("/path", "operation already exists")
	}
	operationKey := uuid.New().String()
	operation := map[string]any{
		apidesign.OperationKey: operationKey,
		"responses":            map[string]any{"200": map[string]any{"description": "OK"}},
	}
	if command.Label != "" {
		operation["summary"] = command.Label
	}
	pathItem[method] = operation
	raw, err := jsonx.Marshal(api)
	if err != nil {
		return err
	}
	contract.Document = raw
	message.Operation = &OperationBinding{ContractID: contract.ID, OperationKey: operationKey}
	return nil
}

func (r *Repo) importContract(ctx context.Context, tx *sql.Tx, document *Document, command Command) error {
	if command.DesignID <= 0 {
		return invalidAt("/designId", "designId must be positive")
	}
	if command.Mode != "copy" && command.Mode != "linked" {
		return invalidAt("/mode", "mode must be copy or linked")
	}
	design, err := r.designs.DetailTx(ctx, tx, command.DesignID)
	if err != nil {
		return invalidAt("/designId", "API design does not exist")
	}
	revision := design.Draft
	if command.RevisionID != nil {
		revision, err = r.designs.RevisionTx(ctx, tx, command.DesignID, *command.RevisionID)
		if err != nil {
			return invalidAt("/revisionId", "API revision does not exist")
		}
	}
	contractID := command.ID
	if contractID == "" {
		contractID = uuid.New().String()
	}
	if findContract(document.Contracts, contractID) != nil {
		return invalidAt("/id", "contract id already exists")
	}
	document.Contracts = append(document.Contracts, Contract{
		ID:       contractID,
		Name:     design.Design.Name,
		Document: jsonx.RawMessage(revision.Document),
		Mode:     command.Mode,
		Source:   &ContractSource{DesignID: command.DesignID, RevisionID: revision.ID, Version: revision.Version},
	})
	return nil
}

func (r *Repo) refreshContract(ctx context.Context, tx *sql.Tx, document *Document, command Command) error {
	contract := findContract(document.Contracts, command.ContractID)
	if contract == nil {
		return invalidAt("/contractId", "contract does not exist")
	}
	if contract.Source == nil {
		return invalidAt("/contractId", "contract has no API source")
	}
	design, err := r.designs.DetailTx(ctx, tx, contract.Source.DesignID)
	if err != nil {
		return invalidAt("/contractId", "API design does not exist")
	}
	revision := design.Draft
	if command.RevisionID != nil {
		revision, err = r.designs.RevisionTx(ctx, tx, contract.Source.DesignID, *command.RevisionID)
		if err != nil {
			return invalidAt("/revisionId", "API revision does not exist")
		}
	}
	contract.Document = jsonx.RawMessage(revision.Document)
	contract.Source = &ContractSource{DesignID: contract.Source.DesignID, RevisionID: revision.ID, Version: revision.Version}
	return nil
}

func (r *Repo) materializeContract(ctx context.Context, tx *sql.Tx, document *Document, command Command, source string, ownerID *int64) error {
	contract := findContract(document.Contracts, command.ContractID)
	if contract == nil {
		return invalidAt("/contractId", "contract does not exist")
	}
	if contract.Mode == "linked" {
		return invalidAt("/contractId", "contract is already linked")
	}
	design, err := r.designs.CreateTx(ctx, tx, apidesign.CreateInput{
		Name:     contract.Name,
		Document: string(contract.Document),
		Source:   source,
		OwnerID:  ownerID,
	})
	if err != nil {
		if invalid, ok := errors.AsType[*apidesign.InvalidError](err); ok {
			diagnostics := make([]Diagnostic, 0, len(invalid.Diagnostics))
			for _, diagnostic := range invalid.Diagnostics {
				diagnostics = append(diagnostics, Diagnostic{Pointer: "/contractId" + diagnostic.Pointer, Message: diagnostic.Message, Severity: diagnostic.Severity})
			}
			return &InvalidError{Diagnostics: diagnostics}
		}
		return err
	}
	contract.Mode = "linked"
	contract.Document = jsonx.RawMessage(design.Draft.Document)
	contract.Source = &ContractSource{DesignID: design.Design.ID, RevisionID: design.Draft.ID, Version: design.Draft.Version}
	return nil
}
