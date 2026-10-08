package mcp

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

type importSchemaDiagnostic struct {
	Path       string `json:"path"`
	Constraint string `json:"constraint"`
	Message    string `json:"message"`
}

type importSchemaDiagnostics struct {
	full      bool
	scores    map[*jsonschema.ValidationError]int
	seen      map[string]bool
	items     []importSchemaDiagnostic
	truncated bool
}

// Select discriminator-compatible union arms without echoing invalid values.
func compactImportSchemaError(err error) error {
	root, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return err
	}
	d := &importSchemaDiagnostics{scores: map[*jsonschema.ValidationError]int{}, seen: map[string]bool{}, items: []importSchemaDiagnostic{}}
	_ = d.score(root)
	d.visit(root)
	for {
		raw, _ := json.Marshal(struct {
			Code        string                   `json:"code"`
			Format      string                   `json:"format"`
			Diagnostics []importSchemaDiagnostic `json:"diagnostics"`
			Truncated   bool                     `json:"truncated"`
			Help        string                   `json:"help"`
		}{"backend_import_schema_invalid", "import-diagnostics-v1", d.items, d.truncated, "Use diagnose_backend_import_request with the original tool name and arguments for complete paged diagnostics; validate_backend_import_batch checks record semantics without staging."})
		if len(raw) <= 3800 || len(d.items) == 0 {
			return fmt.Errorf("%s", raw)
		}
		d.items = d.items[:len(d.items)-1]
		d.truncated = true
	}
}

func (d *importSchemaDiagnostics) score(e *jsonschema.ValidationError) int {
	if value, ok := d.scores[e]; ok {
		return value
	}
	value := 0
	if len(e.Causes) == 0 {
		value = 1
		keyword := schemaErrorKeyword(e)
		if (keyword == "const" || keyword == "enum") && len(e.InstanceLocation) > 0 && slices.Contains([]string{"op", "kind", "status", "recordType", "mode", "action", "format"}, e.InstanceLocation[len(e.InstanceLocation)-1]) {
			value = 1000
		}
	} else if schemaErrorChoice(e) {
		value = 1 << 30
		for _, child := range e.Causes {
			value = min(value, d.score(child))
		}
	} else {
		for _, child := range e.Causes {
			value += d.score(child)
		}
	}
	d.scores[e] = value
	return value
}

func (d *importSchemaDiagnostics) visit(e *jsonschema.ValidationError) {
	if !d.full && len(d.items) >= 8 {
		d.truncated = true
		return
	}
	if schemaErrorChoice(e) && len(e.Causes) > 0 {
		best := e.Causes[0]
		for _, child := range e.Causes[1:] {
			if d.scores[child] < d.scores[best] {
				best = child
			}
		}
		d.visit(best)
		return
	}
	if len(e.Causes) > 0 {
		for _, child := range e.Causes {
			d.visit(child)
		}
		return
	}
	d.add(e)
}

func (d *importSchemaDiagnostics) add(e *jsonschema.ValidationError) {
	parts := make([]string, len(e.InstanceLocation))
	for i, p := range e.InstanceLocation {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1")
	}
	path := "/" + strings.Join(parts, "/")
	if !d.full && len(path) > 512 {
		path = strings.ToValidUTF8(path[:512], "")
		d.truncated = true
	}
	keyword := schemaErrorKeyword(e)
	message := "Value must satisfy the selected schema's " + keyword + " constraint"
	if extra, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
		names, _ := json.Marshal(slices.Sorted(slices.Values(extra.Properties)))
		message = "Additional properties are not allowed: " + string(names)
	}
	if slices.Contains([]string{"required", "type", "maxItems", "minItems", "maxLength", "minLength"}, keyword) {
		message = e.Error()
	}
	if !d.full && len(message) > 300 {
		message = strings.ToValidUTF8(message[:300], "")
		d.truncated = true
	}
	key := path + "\x00" + keyword
	if !d.seen[key] {
		d.seen[key] = true
		d.items = append(d.items, importSchemaDiagnostic{Path: path, Constraint: keyword, Message: message})
	}
}

func schemaErrorKeyword(e *jsonschema.ValidationError) string {
	path := e.ErrorKind.KeywordPath()
	if len(path) == 0 {
		return "schema"
	}
	return path[0]
}
func schemaErrorChoice(e *jsonschema.ValidationError) bool {
	return slices.Contains([]string{"oneOf", "anyOf"}, schemaErrorKeyword(e))
}
