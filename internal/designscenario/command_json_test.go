package designscenario

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestCommandUnmarshalJSONRequiresFieldsAndRejectsIrrelevantFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "missing discriminator", raw: `{}`, wantErr: "type"},
		{name: "unknown discriminator", raw: `{"type":"rename_everything"}`, wantErr: "unknown command"},
		{name: "missing required title", raw: `{"type":"set_title"}`, wantErr: "title"},
		{name: "irrelevant command field", raw: `{"type":"set_title","title":"Wanted","label":"ignored"}`, wantErr: "label"},
		{name: "null required field", raw: `{"type":"set_title","title":null}`, wantErr: "title"},
		{name: "unknown nested participant field", raw: `{"type":"upsert_participant","participant":{"id":"p","name":"P","kind":"user","description":"","extra":true}}`, wantErr: "extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var command Command
			err := jsonx.Unmarshal([]byte(tt.raw), &command)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestCommandUnmarshalJSONPreservesPresentEmptyTitle(t *testing.T) {
	t.Parallel()
	var command Command
	if err := jsonx.Unmarshal([]byte(`{"type":"set_title","title":""}`), &command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "set_title" || command.Title != "" {
		t.Fatalf("command=%+v", command)
	}
}
