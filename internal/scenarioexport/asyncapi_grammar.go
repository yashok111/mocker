package scenarioexport

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/yamlx"
)

// Source: https://raw.githubusercontent.com/asyncapi/spec-json-schemas/master/schemas/3.0.0.json
// AsyncAPI Initiative, Apache-2.0. Pinned SHA-256 below; no runtime loading.
//
//go:embed schemas/asyncapi-3.0.0.json
var asyncAPISchemaBytes []byte

const asyncAPISchemaURI = "http://asyncapi.com/definitions/3.0.0/asyncapi.json"
const asyncAPISchemaSHA256 = "1786a007ac00344a8f529e5a6fc5db786b0be6757e0b94a8b95a8a6a52b7fe9a"

type offlineLoader struct{}

func (offlineLoader) Load(uri string) (any, error) {
	return nil, fmt.Errorf("network and file schema loading disabled: %s", uri)
}

var asyncAPIGrammar struct {
	sync.Once
	schema *jsonschema.Schema
	err    error
}

func officialAsyncAPIGrammar() (*jsonschema.Schema, error) {
	asyncAPIGrammar.Do(func() {
		hash := sha256.Sum256(asyncAPISchemaBytes)
		if fmt.Sprintf("%x", hash) != asyncAPISchemaSHA256 {
			asyncAPIGrammar.err = errors.New("embedded AsyncAPI grammar checksum mismatch")
			return
		}
		value, err := decodeJSON(asyncAPISchemaBytes)
		if err != nil {
			asyncAPIGrammar.err = err
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(offlineLoader{})
		if err = compiler.AddResource(asyncAPISchemaURI, value); err == nil {
			asyncAPIGrammar.schema, err = compiler.Compile(asyncAPISchemaURI)
		}
		asyncAPIGrammar.err = err
	})
	return asyncAPIGrammar.schema, asyncAPIGrammar.err
}

func decodeJSON(raw []byte) (any, error) {
	d := jsonx.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected exactly one JSON value")
	}
	return value, nil
}

func convertAsyncAPIYAML(raw []byte, maxBytes int64) ([]byte, error) {
	out, err := yamlx.FromJSONLimit(raw, maxBytes)
	if errors.Is(err, yamlx.ErrOutputTooLarge) {
		return nil, ErrTooLarge
	}
	return out, err
}
