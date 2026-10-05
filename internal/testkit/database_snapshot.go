package testkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yashok111/mocker/internal/store"
)

// Cache bytes, never an open database or a test's records. Close checkpoints
// the WAL before reading the image. Each fixture gets its own file and pools;
// migration tests that open old schemas directly still exercise real upgrades.
var emptyDatabase = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "mocker-test-schema-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	path := filepath.Join(dir, "schema.db")
	ctx := context.Background() // Process-wide immutable setup, not any test's lifetime.
	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(db.Migrate(ctx, nil), db.Close()); err != nil {
		return nil, err
	}
	return root.ReadFile("schema.db")
})

func seedDB(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil // Reopen/restart tests must retain their existing data.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := emptyDatabase()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	stage, err := os.CreateTemp(dir, ".mocker-fixture-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(stage.Name()) }()
	_, writeErr := stage.Write(data)
	if err := errors.Join(writeErr, stage.Close()); err != nil {
		return err
	}
	// Publish a complete private file atomically without overwriting an existing
	// path, even if two test helpers try to open that same path concurrently.
	if err := os.Link(stage.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
