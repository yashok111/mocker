//go:build workbenchpreview

package backendmodel

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
)

// Export inert, independently reviewed Orders import inputs for manual browser
// QA. This helper never connects to or executes the imported application.
func TestWorkbenchOrdersPreviewInputs(t *testing.T) {
	dir := os.Getenv("WORKBENCH_QA_DIR")
	if dir == "" {
		t.Fatal("WORKBENCH_QA_DIR must name an explicit fixture output directory")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p Project
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		b, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("begin.json", eventsOrdersInput(t, &p, true))
	raw, err = os.ReadFile(filepath.Join(dir, "import.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var session ImportSession
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	commands := eventsOrdersCommands(t, &session, true)
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	write("batch.json", ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
}
