package backendmodel

import (
	"encoding/json/v2"
	"os"
	"testing"
)

// The browser imports these same vectors to check its independent encoder.
// Keep literal canonical bytes and hashes so drift in either implementation fails.
func TestImportBatchBrowserHashVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/import_batch_hash_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string          `json:"name"`
		Commands  []ImportCommand `json:"commands"`
		Canonical string          `json:"canonical"`
		SHA256    string          `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 4 {
		t.Fatal("browser hash witness set is incomplete")
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			canonical, err := canonicalJSON(vector.Commands)
			if err != nil || string(canonical) != vector.Canonical {
				t.Fatalf("canonical command bytes changed: %s (%v)", canonical, err)
			}
			hash, err := ImportBatchHash(vector.Commands)
			if err != nil || hash != vector.SHA256 {
				t.Fatalf("browser/server command hash changed: %s (%v)", hash, err)
			}
		})
	}
}
