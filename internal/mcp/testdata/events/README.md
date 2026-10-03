# Orders public SDK import fixture

`orders-import.json` is inert import data for
`TestBackendEventsRealSDKGuideFixtureExample`. It contains the full 433-command
initial source scope, the resolved manifest, 16 keyed replacements/additions and
six omitted obsolete node/edge/evidence commands. The second import reconstructs
all 442 source commands, then explicitly deletes three obsolete assertions by
their acknowledged expected IDs. Replacement identity is record type + external
key; equal keys in different record namespaces never collide.

The single source tree and independently authored expectations remain in
`internal/backendmodel/testdata/events/orders/`. No source or oracle is copied
here. The public test verifies each original source hash and every analyzed
member/evidence hash, physical line pair and snippet before staging. Fixture
TypeScript is never imported or executed. Product tests read no `docs/` or
`/tmp` artifacts, and use one shared real SDK server per example test.

## Provenance and reproduction

The template was exported on 2026-10-03 from the reviewed test-only transcription
`eventsOrdersInput` / `eventsOrdersCommands` in
`internal/backendmodel/events_orders_fixture_test.go`. The independent
`expected.json` oracle supplies expected routes/handlers and source hashes;
exported command output never supplies those expectations. Reconcile completion
of files/endpoints/datastores means enumeration within the five supplied members;
database schema, dynamic audit remainder and private-ledger source remain unknown.
The example is public product QA. Ordinary/live-agent A21/S19 remains deferred.

To reproduce without adding an exporter to product code, put the following Go
file in a temporary directory and map a virtual
`internal/backendmodel/events_public_export_overlay_test.go` to it using the Go
`-overlay` JSON Replace map. Set `MOCKER_EVENTS_TEMPLATE_EXPORT` to a temporary
output path, then run `go test -overlay OVERLAY.json ./internal/backendmodel
-run '^TestEventsPublicExport$' -count=1`. This runs the source transcription only;
no application fixture executes.

```go
package backendmodel

import (
    "encoding/json/v2"
    "os"
    "testing"
)

func TestEventsPublicExport(t *testing.T) {
    p := &Project{ID: "11111111-1111-7111-8111-111111111111", Version: 1,
        CurrentRevisionID: "22222222-2222-7222-8222-222222222222"}
    phases := map[string]any{}
    for _, resolved := range []bool{false, true} {
        in := eventsOrdersInput(t, p, resolved)
        s := &ImportSession{ID: "33333333-3333-7333-8333-333333333333",
            ProjectID: p.ID, RepositoryID: "44444444-4444-7444-8444-444444444444",
            SnapshotID: "55555555-5555-7555-8555-555555555555", Profile: in.Profile,
            Mode: "initial", Manifest: in.Manifest, Inventory: in.Inventory, Version: 1}
        phase := "baseline"
        if resolved { phase = "resolved" }
        phases[phase] = map[string]any{"input": in, "commands": eventsOrdersCommands(t, s, resolved)}
    }
    out := map[string]any{"phases": phases, "placeholders": map[string]string{
        "projectId": p.ID, "baseRevisionId": p.CurrentRevisionID,
        "importId": "33333333-3333-7333-8333-333333333333",
        "repositoryId": "44444444-4444-7444-8444-444444444444",
        "snapshotId": "55555555-5555-7555-8555-555555555555"}}
    raw, err := json.Marshal(out)
    if err != nil { t.Fatal(err) }
    if err := os.WriteFile(os.Getenv("MOCKER_EVENTS_TEMPLATE_EXPORT"), raw, 0600); err != nil { t.Fatal(err) }
}
```

Compact the exported JSON with this Python procedure (`exported.json` is that
fresh output), then compare it to the checked-in fixture before changing it:

```python
import json
from pathlib import Path
x = json.loads(Path("exported.json").read_text())
b = x["phases"]["baseline"]
r = x["phases"]["resolved"]
def address(c):
    return (c["op"], next(c[k]["externalKey"] for k in ("node", "edge", "evidence") if k in c))
old = {address(c): c for c in b["commands"]}
new = {address(c) for c in r["commands"]}
out = {
    "placeholders": x["placeholders"], "baseline": b,
    "resolvedInput": r["input"],
    "resolvedUpdates": [c for c in r["commands"] if old.get(address(c)) != c],
    "resolvedOmitted": [list(address(c)) for c in b["commands"] if address(c) not in new],
}
Path("orders-import.json").write_text(json.dumps(out, separators=(",", ":")) + "\n")
```

Do not regenerate the independent source oracle from this template. A source
change needs separate review of physical declarations, typed transcription and
expected witnesses. Rerun the public SDK example after any accepted update.
