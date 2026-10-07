// Command build builds a variant and its independently checkable source/build manifest.
package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
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
	c := exec.Command("go", args...)
	c.Env = append(os.Environ(), "GOFLAGS=", "CGO_ENABLED=0", "GOWORK=off", "GOENV=off")
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("go command failed: %w", err)
	}
	return out, nil
}
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
	envKeys := []string{"GOVERSION", "GOOS", "GOARCH", "GOEXPERIMENT", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM", "GOFIPS140"}
	envRaw, err := command(append([]string{"env", "-json"}, envKeys...)...)
	if err != nil {
		return err
	}
	buildEnv := map[string]string{}
	if err = json.Unmarshal(envRaw, &buildEnv); err != nil {
		return err
	}
	raw, err := command("list", "-mod=readonly", "-pgo=off", "-deps", "-json", "-tags="+tag, "./cmd/orders-reference")
	if err != nil {
		return err
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	paths := map[string]bool{"go.mod": true, "go.sum": true}
	for {
		value, e := decoder.ReadValue()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		var pkg struct {
			Dir                                              string
			Standard                                         bool
			GoFiles, CgoFiles, EmbedFiles, SFiles, SysoFiles []string
		}
		if e = json.Unmarshal(value, &pkg); e != nil {
			return e
		}
		if pkg.Standard {
			continue
		}
		rel, e := filepath.Rel(root, pkg.Dir)
		if e != nil {
			return e
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
	m := manifest{Policy: p.ManifestPolicy, Files: []p.SourceFile{}}
	for path := range paths {
		for part := path; part != "."; part = filepath.Dir(part) {
			info, e := os.Lstat(part)
			if e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink source: %s", part)
			}
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular source: %s", path)
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		m.Files = append(m.Files, p.SourceFile{Path: path, SHA256: p.HashBytes(data)})
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
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
	if e = os.MkdirAll(out, 0700); e != nil {
		return e
	}
	binary := filepath.Join(out, "orders-reference-"+variant)
	_, e = command("build", "-mod=readonly", "-pgo=off", "-trimpath", "-buildvcs=false", "-tags="+tag, "-ldflags=-X main.buildHash="+m.BuildHash+" -X main.sourceTreeHash="+source, "-o", binary, "./cmd/orders-reference")
	if e != nil {
		return e
	}
	for _, file := range m.Files {
		data, e := os.ReadFile(file.Path)
		if e != nil {
			return e
		}
		if p.HashBytes(data) != file.SHA256 {
			return fmt.Errorf("source changed during build: %s", file.Path)
		}
	}
	data, e := os.ReadFile(binary)
	if e != nil {
		return e
	}
	m.BinarySHA256 = p.HashBytes(data)
	data, e = p.Encode(m)
	if e != nil {
		return e
	}
	if e = os.WriteFile(binary+".manifest.json", data, 0600); e != nil {
		return e
	}
	fmt.Printf("%s source=%s build=%s binary=%s\n", variant, source, m.BuildHash, m.BinarySHA256)
	return nil
}
