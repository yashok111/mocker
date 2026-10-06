package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBackendStorageCLIRejectsMissingAndInvalidTargets(t *testing.T) {
	for _, args := range [][]string{nil, {"verify"}, {"rebuild", "--db", "missing", "--project", "bad"}, {"verify", "--db", "missing", "--project", "ignored"}, {"verify", "--db", "missing", "extra"}} {
		var out bytes.Buffer
		if err := runBackendStorage(t.Context(), args, &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "absent.db")
	var out bytes.Buffer
	if err := runBackendStorage(t.Context(), []string{"verify", "--db", path}, &out, &out); err == nil {
		t.Fatal("created target")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("CLI created database", err)
	}
}
