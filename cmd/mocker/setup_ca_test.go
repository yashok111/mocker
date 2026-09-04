package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The root is generated once and then served from disk: a second call must
// return the same bytes, never a new key pair — rotating the root behind an
// installed trust is the exact failure this slice exists to prevent.
func TestEnsureLocalCA_isIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := ensureLocalCA(dir)
	if err != nil {
		t.Fatalf("first ensureLocalCA: %v", err)
	}
	if !bytes.Contains(first, []byte("BEGIN CERTIFICATE")) {
		t.Fatalf("first call returned %q, not a PEM certificate", first)
	}
	certStat, err := os.Stat(filepath.Join(dir, "root.crt"))
	if err != nil {
		t.Fatalf("root.crt missing: %v", err)
	}
	keyStat, err := os.Stat(filepath.Join(dir, "root.key"))
	if err != nil {
		t.Fatalf("root.key missing: %v", err)
	}
	if keyStat.Mode().Perm() != 0o600 {
		t.Fatalf("root.key mode = %o, want 0600 (the key signs for every host of the contour)", keyStat.Mode().Perm())
	}

	second, err := ensureLocalCA(dir)
	if err != nil {
		t.Fatalf("second ensureLocalCA: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second call returned different bytes — the root rotated")
	}
	if certStat.ModTime() != mustModTime(t, filepath.Join(dir, "root.crt")).ModTime() {
		t.Fatal("root.crt was rewritten by the second call")
	}
}

func mustModTime(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}
