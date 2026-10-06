package backendblob

import (
	"fmt"
	"strings"
)

type Column struct {
	Name, Type, Default string
	Payload             bool
}
type Object struct{ Kind, Name, SQL string }
type Owner struct {
	Table                                 string
	Columns                               []Column
	PK, Group                             []string
	ProjectColumn, ProjectSQL, ProjectArg string
	OldDDL, DDL                           string
	Objects, OldObjects                   []Object
}
type Exclusion struct {
	Table   string
	Columns []string
	Reason  string
}

// Registry returns a defensive copy of the compiled allowlist. SQL identifiers
// are selected from this list, never from operator or HTTP input.
func Registry() []Owner {
	out := make([]Owner, len(owners))
	for i, o := range owners {
		o.Columns = append([]Column(nil), o.Columns...)
		o.PK = append([]string(nil), o.PK...)
		o.Group = append([]string(nil), o.Group...)
		o.Objects = append([]Object(nil), o.Objects...)
		o.OldObjects = append([]Object(nil), o.OldObjects...)
		out[i] = o
	}
	return out
}
func Exclusions() []Exclusion {
	out := make([]Exclusion, len(exclusions))
	for i, e := range exclusions {
		e.Columns = append([]string(nil), e.Columns...)
		out[i] = e
	}
	return out
}
func Lookup(table string) (Owner, error) {
	for _, o := range owners {
		if o.Table == table {
			return o, nil
		}
	}
	return Owner{}, fmt.Errorf("unregistered immutable owner %q", table)
}
func (c Column) StorageName() string {
	if !c.Payload {
		return c.Name
	}
	if c.Name == "document" {
		return "payload_key"
	}
	return c.Name + "_payload_key"
}
func (o Owner) ViewSQL() string {
	var cols []string
	for _, c := range o.Columns {
		expr := "r." + c.Name
		if c.Payload {
			value := "CAST(payload AS TEXT)"
			if c.Type == "BLOB" {
				value = "payload"
			}
			expr = "(SELECT " + value + " FROM backend_payload_blobs WHERE key=r." + c.StorageName() + ") AS " + c.Name
		}
		cols = append(cols, expr)
	}
	if o.preservesOrdinal() {
		cols = append(cols, "r.rowid AS rowid")
	}
	return "CREATE VIEW " + o.Table + "_documents AS SELECT " + strings.Join(cols, ",") + " FROM " + o.Table + " r"
}
func (o Owner) column(name string) (Column, bool) {
	for _, c := range o.Columns {
		if c.Name == name || c.StorageName() == name {
			return c, true
		}
	}
	return Column{}, false
}
func (o Owner) storageColumns() []string {
	cols := make([]string, len(o.Columns))
	for i, c := range o.Columns {
		cols[i] = c.StorageName()
	}
	return cols
}

// Trusted replay steps historically use insertion order during reconciliation.
func (o Owner) preservesOrdinal() bool {
	return strings.TrimSuffix(o.Table, "_blob_rebuild") == "backend_replay_steps"
}
func (o Owner) projectionColumns() []string {
	cols := o.storageColumns()
	if o.preservesOrdinal() {
		cols = append(cols, "rowid")
	}
	return cols
}
func (o Owner) metadataCount() int { return len(o.projectionColumns()) }

func (o Owner) payloadCount() int {
	n := 0
	for _, c := range o.Columns {
		if c.Payload {
			n++
		}
	}
	return n
}
