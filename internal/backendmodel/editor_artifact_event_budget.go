package backendmodel

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/designscenario"
)

// MaxEditorEventConstructionBytes bounds B31's conservative construction
// estimate BEFORE the existing pure owner analyzer. It is independent of the
// owner's final encoded 4MiB report and the new frozen context 4MiB admission.
const MaxEditorEventConstructionBytes = 4 << 20

type editorEventConstruction struct {
	Nodes, Edges, Diagnostics int
	Bytes                     int64
	exceeded                  string
}

// Charges reserve 1KiB per potential row for struct slots, map entries, growing
// slice capacity and fixed pointer/diagnostic text. Variable strings are charged
// as escaped wire bytes, including each repetition in generated IDs/locators.
// This is a construction work estimate, NOT an exact heap measurement or an
// aggregate page limit. Decoded input/snapshot/tree work is accounted separately.
func (b *editorEventConstruction) add(nodes, edges, diagnostics int, variable int64) bool {
	b.Nodes += nodes
	b.Edges += edges
	b.Diagnostics += diagnostics
	b.Bytes += int64(nodes+edges+diagnostics)*1024 + variable
	switch {
	case b.Nodes > designscenario.MaxEventMapNodes:
		b.exceeded = "construction_nodes"
	case b.Edges > designscenario.MaxEventMapEdges:
		b.exceeded = "construction_edges"
	case b.Diagnostics > designscenario.MaxEventMapDiagnostics:
		b.exceeded = "construction_diagnostics"
	case b.Bytes > MaxEditorEventConstructionBytes:
		b.exceeded = "construction_bytes"
	}
	return b.exceeded == ""
}
func editorEscapedBytes(s string, id bool) int64 {
	var n int64
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch {
		case id && (r == '%' || r == ':'):
			n += 3
		case r == '"' || r == '\\':
			n += 2
		case r < 32 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			n += 6
		default:
			n += int64(size)
		}
	}
	return n
}
func editorIDBytes(parts ...string) int64 {
	var n int64
	for _, p := range parts {
		n += editorEscapedBytes(p, true) + 1
	}
	return n
}

// The ownership edge ID embeds an already escaped participant node ID. Count
// its second %/: expansion without materializing either generated string.
func editorNestedIDBytes(parts ...string) int64 {
	var n int64
	for _, p := range parts {
		n += editorEscapedBytes(p, true) + 2*int64(strings.Count(p, "%")+strings.Count(p, ":")) + 3
	}
	return n
}
func editorLabel(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

type editorHTTPConstruction struct{ path, pointer, label int64 }

// editorEventPreflight uses only already decoded immutable authored values.
// It performs no owner writes, simulations, full EventMap construction or raw
// document duplication. Traversals stop immediately once admission fails.
func editorEventPreflight(ctx context.Context, s *editorOwnerSnapshot) (editorEventConstruction, error) {
	b := editorEventConstruction{}
	doc := s.scenario.Document
	m := doc.EventModel
	if m == nil {
		return b, ctx.Err()
	}
	http := map[string]editorHTTPConstruction{}
	// The analyzer decodes embedded HTTP documents and re-encodes state extensions
	// before decoding them. Reserve escaped bytes before either representation is
	// allocated; all trees below still alias the request's immutable decoded input.
	for _, c := range doc.Contracts {
		if err := ctx.Err(); err != nil {
			return b, err
		}
		if !b.add(0, 0, 0, 6*int64(len(c.Document))) {
			return b, nil
		}
	}
	if err := b.chargeHTTPContracts(ctx, s, http); err != nil || b.exceeded != "" {
		return b, err
	}
	if err := b.chargeEventOwners(ctx, doc); err != nil || b.exceeded != "" {
		return b, err
	}
	messages := map[string]designscenario.EventMessage{}
	if err := b.chargeEventEntities(ctx, m, messages); err != nil || b.exceeded != "" {
		return b, err
	}
	if err := b.chargeEventOperations(ctx, m, messages, http); err != nil || b.exceeded != "" {
		return b, err
	}
	return b, ctx.Err()
}

func (b *editorEventConstruction) chargeHTTPContracts(ctx context.Context, s *editorOwnerSnapshot, http map[string]editorHTTPConstruction) error {
	// Bound the owner's effective Path Item materialization before it merges maps
	// or copies inherited parameter slices. Each chain is traversed directly from
	// the cached lossless tree. 128 is the existing PathItems depth ceiling.
	for _, cv := range editorArray(s.tree.root["contracts"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := editorObject(cv)
		root := editorObject(c["document"])
		entry := editorHTTPConstruction{}
		for path, value := range editorObject(root["paths"]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !strings.HasPrefix(path, "/") {
				continue
			}
			pathBytes := editorEscapedBytes(path, false)
			entry.path = max(entry.path, pathBytes)
			// EscapePointer may double slash/tilde bytes before JSON string escaping.
			pointerBytes := int64(32) + 2*pathBytes
			entry.pointer = max(entry.pointer, pointerBytes)
			if err := b.chargeHTTPPath(ctx, root, value, pathBytes, pointerBytes, &entry); err != nil || b.exceeded != "" {
				return err
			}
		}
		http[editorString(c["id"])] = entry
	}
	return nil
}

func (b *editorEventConstruction) chargeEventOwners(ctx context.Context, doc designscenario.Document) error {
	m := doc.EventModel
	owners := map[string]bool{}
	for _, c := range m.Contracts {
		owners[c.ParticipantID] = true
	}
	for _, p := range doc.Participants {
		if err := ctx.Err(); err != nil {
			return err
		}
		if owners[p.ID] {
			id := editorIDBytes("participant", p.ID)
			if !b.add(1, 0, 1, 2*id+editorEscapedBytes(editorLabel(p.Name, p.ID), false)) {
				return nil
			}
		}
	}
	for _, server := range m.Servers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !b.add(1, 0, 1, 2*editorIDBytes("server", server.ID)+editorEscapedBytes(editorLabel(server.Name, editorLabel(server.Host, server.ID)), false)) {
			return nil
		}
	}
	return nil
}

func (b *editorEventConstruction) chargeEventEntities(ctx context.Context, m *designscenario.EventModel, messages map[string]designscenario.EventMessage) error {
	for _, msg := range m.Messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		messages[msg.ID] = msg
		id := editorIDBytes("message", msg.ID)
		if !b.add(1, 0, 1, 2*id+editorEscapedBytes(editorLabel(msg.Name, msg.ID), false)) {
			return nil
		}
		for _, ref := range []string{msg.PayloadSchemaID, msg.KeySchemaID, msg.HeadersSchemaID} {
			if ref != "" && !b.add(0, 1, 1, 2*(id+editorIDBytes("schema", ref))) {
				return nil
			}
		}
	}
	for _, schema := range m.Schemas {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !b.add(1, 0, 2, 2*editorIDBytes("schema", schema.ID)+editorEscapedBytes(editorLabel(schema.Name, schema.ID), false)) {
			return nil
		}
	}
	for _, ch := range m.Channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := editorIDBytes("channel", ch.ID)
		if !b.add(1, 0, 1, 2*id+editorEscapedBytes(editorLabel(ch.Address, editorLabel(ch.Name, ch.ID)), false)) {
			return nil
		}
		for _, ref := range ch.ServerIDs {
			if !b.add(0, 1, 1, 2*(id+editorIDBytes("server", ref))) {
				return nil
			}
		}
		for _, ref := range ch.MessageIDs {
			if !b.add(0, 1, 3, 2*(id+editorIDBytes("message", ref))) {
				return nil
			}
		}
	}
	return nil
}

func (b *editorEventConstruction) chargeEventOperations(ctx context.Context, m *designscenario.EventModel, messages map[string]designscenario.EventMessage, http map[string]editorHTTPConstruction) error {
	for _, c := range m.Contracts {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, op := range c.Operations {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := editorIDBytes("operation", c.ID, op.ID)
			msg := messages[op.MessageID]
			label := editorEscapedBytes(editorLabel(op.Name, op.ID), false) + editorEscapedBytes(editorLabel(msg.Name, msg.ID), false) + 8
			// Every operation may generate missing participant/channel/message/action,
			// unbound/group, mismatched message and route warnings. Bounds intentionally
			// reserve these even for valid rows so malformed saved content is bounded.
			participant := editorIDBytes("participant", c.ParticipantID) + editorNestedIDBytes("participant", c.ParticipantID) + 3*editorEscapedBytes(c.ParticipantID, false)
			if !b.add(1, 2, 8, 4*id+label+participant+2*editorIDBytes("channel", op.ChannelID)) {
				return nil
			}
			if op.Kafka != nil {
				if !b.add(0, 0, 0, editorEscapedBytes(op.Kafka.ClientID, false)+editorEscapedBytes(op.Kafka.GroupID, false)) {
					return nil
				}
			}
			if op.FailureRoutes != nil {
				for _, ref := range []string{op.FailureRoutes.RetryChannelID, op.FailureRoutes.DeadLetterChannelID} {
					if ref != "" && !b.add(0, 1, 4, 2*(id+editorIDBytes("channel", ref))) {
						return nil
					}
				}
			}
			for _, link := range op.APILinks {
				if err := ctx.Err(); err != nil {
					return err
				}
				entry := http[link.ContractID]
				target := editorIDBytes("api_operation", link.ContractID, link.OperationKey)
				// The owner constructs every duplicate target before sorting/compacting;
				// labels and authored pointers therefore cost once PER short source link.
				if !b.add(1, 1, 1, 6*(id+target)+2*entry.label+3*entry.pointer+2*entry.path+4*editorEscapedBytes(link.ContractID, false)+4*editorEscapedBytes(link.OperationKey, false)) {
					return nil
				}
			}
			for _, link := range op.StateLinks {
				if err := ctx.Err(); err != nil {
					return err
				}
				target := editorIDBytes("state_transition", link.ContractID, link.DiagramID, link.TransitionID)
				// State names are each <=240 bytes; owner pointer includes only indexes and
				// extension literals. Contract IDs recur in target IDs and both locators.
				if !b.add(1, 1, 2, 6*(id+target)+4096+4*editorEscapedBytes(link.ContractID, false)) {
					return nil
				}
			}
		}
	}
	return nil
}

func (b *editorEventConstruction) chargeHTTPPath(ctx context.Context, root map[string]any, value any, pathBytes, pointerBytes int64, entry *editorHTTPConstruction) error {
	current := value
	seen := map[string]bool{}
	for depth := 0; depth < 128; depth++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := editorObject(current)
		if item == nil {
			break
		}
		// Conservative merged map allocation, inherited parameter array capacity,
		// operation records and source pointer/key materialization for all methods.
		parameters := len(editorArray(item["parameters"]))
		bytes := int64(len(item))*256 + int64(parameters)*32
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			if op := editorObject(item[method]); op != nil {
				bytes += 640 + pointerBytes + editorEscapedBytes(editorString(op["x-mocker-canvas-operation-id"]), false)
				entry.label = max(entry.label, pathBytes+8)
			}
		}
		if !b.add(0, 0, 0, bytes) {
			return nil
		}
		ref := editorString(item["$ref"])
		if !strings.HasPrefix(ref, "#/") || seen[ref] {
			break
		}
		seen[ref] = true
		// An inherited pointer itself can be much wider than the concrete path.
		pointerBytes = int64(32) + editorEscapedBytes(ref, false)
		entry.pointer = max(entry.pointer, pointerBytes)
		var found bool
		current, found = editorHTTPReference(root, ref[1:])
		if !found {
			break
		}
	}
	return nil
}

// Missing or invalid inherited references end conservative chain traversal.
func editorHTTPReference(root map[string]any, pointer string) (any, bool) {
	value, err := editorAt(root, pointer)
	return value, err == nil
}
