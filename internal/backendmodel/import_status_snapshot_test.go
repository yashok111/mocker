package backendmodel

import (
	"context"
	"testing"
	"time"
)

func TestImportStatusUsesOneReaderSnapshot(t *testing.T) {
	r, p, session, _ := committedBase(t)
	r.db.R.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	status, err := r.Import(ctx, p.ID, session.ID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.State != "committed" || status.Preview == nil || status.Preview.Version != status.Session.Version-1 || status.CommittedRevisionID == nil || *status.CommittedRevisionID != p.CurrentRevisionID || len(status.AcceptedBatches) == 0 {
		t.Fatalf("inconsistent committed snapshot: %+v", status)
	}
}
