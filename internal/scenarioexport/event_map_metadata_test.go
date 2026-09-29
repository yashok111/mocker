package scenarioexport

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestEventMapMetadataDoesNotChangeAsyncAPIExport(t *testing.T) {
	t.Parallel()
	for _, format := range []Format{AsyncAPIJSON, AsyncAPIYAML} {
		for _, blocked := range []bool{false, true} {
			rev := eventFixture(t)
			if blocked {
				rev.Document.EventModel.Schemas[0].SchemaJSON = `{"type":"object","unevaluatedProperties":false}`
			}
			retry := rev.Document.EventModel.Channels[0]
			retry.ID, retry.Name, retry.Address = "retry", "Retry", "orders.retry"
			rev.Document.EventModel.Channels = append(rev.Document.EventModel.Channels, retry)
			svc := New(nil, 16<<20)
			request := Request{Format: format, ContractID: "notificationsContract"}
			before, beforeErr := svc.Export(rev, request)
			op := &rev.Document.EventModel.Contracts[1].Operations[0]
			op.FailureRoutes = &designscenario.EventFailureRoutes{RetryChannelID: "retry"}
			op.APILinks = []designscenario.EventAPILink{{ContractID: "missing", OperationKey: "create"}}
			op.StateLinks = []designscenario.EventStateLink{{ContractID: "missing", DiagramID: "orders", TransitionID: "placed"}}
			after, afterErr := svc.Export(rev, request)
			if blocked {
				var a, b *BlockedError
				if !errors.As(beforeErr, &a) || !errors.As(afterErr, &b) || !reflect.DeepEqual(a.Diagnostics, b.Diagnostics) {
					t.Fatalf("metadata changed export diagnostics: %v / %v", beforeErr, afterErr)
				}
			} else if beforeErr != nil || afterErr != nil {
				t.Fatalf("export failed: %v / %v", beforeErr, afterErr)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s export changed after metadata: before=%+v after=%+v", format, before, after)
			}
		}
	}
}
