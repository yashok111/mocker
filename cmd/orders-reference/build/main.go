// Command build builds a variant and its independently checkable source/build manifest.
package main

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

type manifest struct {
	Policy       string            `json:"policy"`
	Files        []p.SourceFile    `json:"files"`
	Descriptor   p.BuildDescriptor `json:"descriptor"`
	BuildHash    string            `json:"buildHash"`
	BinarySHA256 string            `json:"binarySHA256"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func command(args ...string) ([]byte, error) {
	c := exec.CommandContext(context.Background(), "go", args...) //nolint:gosec // G204: the go tool with arguments this command assembles itself; nothing comes from a request
	c.Env = append(os.Environ(), "GOFLAGS=", "CGO_ENABLED=0", "GOWORK=off", "GOENV=off")
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("go command failed: %w", err)
	}
	return out, nil
}

var envKeys = []string{"GOVERSION", "GOOS", "GOARCH", "GOEXPERIMENT", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM", "GOFIPS140"}

func run() error {
	if len(os.Args) != 3 || (os.Args[1] != "buggy" && os.Args[1] != "fixed") {
		return fmt.Errorf("usage: go run ./cmd/orders-reference/build buggy|fixed OUTPUT_DIRECTORY")
	}
	variant, out := os.Args[1], os.Args[2]
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	tag := "orders_" + variant
	envRaw, err := command(append([]string{"env", "-json"}, envKeys...)...)
	if err != nil {
		return err
	}
	buildEnv := map[string]string{}
	if err = json.Unmarshal(envRaw, &buildEnv); err != nil {
		return err
	}
	paths, err := sourcePaths(root, tag)
	if err != nil {
		return err
	}
	m := manifest{Policy: p.ManifestPolicy}
	if m.Files, err = hashSources(paths); err != nil {
		return err
	}
	source, e := p.SourceTreeHash(m.Files)
	if e != nil {
		return e
	}
	// Identity substitutions are represented by placeholders to avoid a circular hash.
	flags := []string{"CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOENV=off", "-mod=readonly", "-pgo=off", "-trimpath", "-buildvcs=false", "-tags=" + tag, "-ldflags=-X main.buildHash={buildHash} -X main.sourceTreeHash={sourceTreeHash}"}
	for _, key := range envKeys {
		flags = append(flags, key+"="+buildEnv[key])
	}
	m.Descriptor = p.BuildDescriptor{ServiceVersion: "1.0.0", Variant: variant, SourceTreeHash: source, Toolchain: buildEnv["GOVERSION"], GOOS: buildEnv["GOOS"], GOARCH: buildEnv["GOARCH"], BuildFlags: flags}
	m.BuildHash, e = p.BuildHash(m.Descriptor)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(out, 0700); e != nil { //nolint:gosec // G703: the output directory is the operator's own command-line argument
		return e
	}
	binary := filepath.Join(out, "orders-reference-"+variant)
	_, e = command("build", "-mod=readonly", "-pgo=off", "-trimpath", "-buildvcs=false", "-tags="+tag, "-ldflags=-X main.buildHash="+m.BuildHash+" -X main.sourceTreeHash="+source, "-o", binary, "./cmd/orders-reference")
	if e != nil {
		return e
	}
	if e = checkSourcesUnchanged(m.Files); e != nil {
		return e
	}
	data, e := os.ReadFile(binary) //nolint:gosec // G304: the binary this command just built under the operator's output directory
	if e != nil {
		return e
	}
	m.BinarySHA256 = p.HashBytes(data)
	data, e = p.Encode(m)
	if e != nil {
		return e
	}
	if e = os.WriteFile(binary+".manifest.json", data, 0600); e != nil { //nolint:gosec // G703: next to the binary, under the operator's output directory
		return e
	}
	fmt.Printf("%s source=%s build=%s binary=%s\n", variant, source, m.BuildHash, m.BinarySHA256)
	return nil
}

// sourcePaths lists, relative to root, every non-standard source file the
// tagged build compiles, plus go.mod and go.sum; dependencies outside the
// module tree are pinned by go.sum instead.
func sourcePaths(root, tag string) (map[string]bool, error) {
	raw, err := command("list", "-mod=readonly", "-pgo=off", "-deps", "-json", "-tags="+tag, "./cmd/orders-reference")
	if err != nil {
		return nil, err
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	paths := map[string]bool{"go.mod": true, "go.sum": true}
	for {
		value, e := decoder.ReadValue()
		if errors.Is(e, io.EOF) {
			return paths, nil
		}
		if e != nil {
			return nil, e
		}
		var pkg struct {
			Dir                                              string
			Standard                                         bool
			GoFiles, CgoFiles, EmbedFiles, SFiles, SysoFiles []string
		}
		if e = json.Unmarshal(value, &pkg); e != nil {
			return nil, e
		}
		if pkg.Standard {
			continue
		}
		rel, e := filepath.Rel(root, pkg.Dir)
		if e != nil {
			return nil, e
		}
		if rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		for _, list := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.EmbedFiles, pkg.SFiles, pkg.SysoFiles} {
			for _, name := range list {
				paths[filepath.ToSlash(filepath.Join(rel, name))] = true
			}
		}
	}
}

// hashSources hashes each source file, refusing a symlink anywhere on its
// path and anything that is not a regular file, sorted by path.
func hashSources(paths map[string]bool) ([]p.SourceFile, error) {
	files := []p.SourceFile{}
	for path := range paths {
		for part := path; part != "."; part = filepath.Dir(part) {
			info, e := os.Lstat(part)
			if e != nil {
				return nil, e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlink source: %s", part)
			}
		}
		info, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("nonregular source: %s", path)
		}
		data, e := os.ReadFile(path) //nolint:gosec // G304: a module source file named by go list, checked regular and symlink-free above
		if e != nil {
			return nil, e
		}
		files = append(files, p.SourceFile{Path: path, SHA256: p.HashBytes(data)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// checkSourcesUnchanged re-hashes the sources after the build, so a file
// edited while it compiled cannot be attributed to the wrong manifest.
func checkSourcesUnchanged(files []p.SourceFile) error {
	for _, file := range files {
		data, e := os.ReadFile(file.Path)
		if e != nil {
			return e
		}
		if p.HashBytes(data) != file.SHA256 {
			return fmt.Errorf("source changed during build: %s", file.Path)
		}
	}
	return nil
}
