package ordersprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strings"
	"uuid"
)

func ValidID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s && s != "00000000-0000-0000-0000-000000000000"
}
func ValidHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func HashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Hash freezes deterministic JSON for typed DTOs only, with a domain separator.
func Hash(domain string, v any) (string, error) {
	b, e := json.Marshal(v, json.Deterministic(true))
	if e != nil {
		return "", e
	}
	return HashBytes(append([]byte(domain+"\n"), b...)), nil
}
func IdentityHash(v Identity) (string, error) {
	if e := v.Validate(); e != nil {
		return "", e
	}
	return Hash("orders-identity-v1", v)
}
func FixtureHash() string { h, _ := Hash(FixtureVersion, Fixture()); return h }
func BuildHash(v BuildDescriptor) (string, error) {
	if !ValidHash(v.SourceTreeHash) || v.ServiceVersion == "" || v.Toolchain == "" || v.GOOS == "" || v.GOARCH == "" || !slices.Contains([]string{"buggy", "fixed"}, v.Variant) {
		return "", fmt.Errorf("invalid build descriptor")
	}
	return Hash("orders-build-v1", v)
}
func SourceTreeHash(files []SourceFile) (string, error) {
	files = slices.Clone(files)
	slices.SortFunc(files, func(a, b SourceFile) int { return strings.Compare(a.Path, b.Path) })
	if len(files) == 0 {
		return "", fmt.Errorf("empty source manifest")
	}
	for i, f := range files {
		if f.Path == "." || f.Path == ".." || path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || strings.HasPrefix(f.Path, "../") || strings.ContainsAny(f.Path, "\\\x00\r\n") || !ValidHash(f.SHA256) || (i > 0 && files[i-1].Path == f.Path) {
			return "", fmt.Errorf("invalid source manifest entry")
		}
	}
	return Hash(ManifestPolicy, files)
}
func (e Endpoint) Route(runID string) (method, route string, err error) {
	if e == IdentityEndpoint {
		if runID != "" && !ValidID(runID) {
			return "", "", fmt.Errorf("invalid run id")
		}
		return "GET", "/__mocker_test/identity", nil
	}
	if !ValidID(runID) {
		return "", "", fmt.Errorf("invalid run id")
	}
	base := "/__mocker_test/runs/" + runID
	switch e {
	case ResetEndpoint:
		return "POST", base + "/reset", nil
	case FailureEndpoint:
		return "POST", base + "/failure", nil
	case OrderEndpoint:
		return "POST", "/orders", nil
	case JournalEndpoint:
		return "GET", base + "/journal", nil
	default:
		return "", "", fmt.Errorf("unknown endpoint")
	}
}

// Decode rejects duplicate/unknown fields, nulls, multiple roots and absent
// required fields, recursively. Optional fields use omitempty or omitzero.
func Decode(raw []byte, out any, limit int) error {
	if limit <= 0 || len(raw) > limit {
		return fmt.Errorf("body limit")
	}
	d := jsontext.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if tok.Kind() == 'n' {
			return fmt.Errorf("null forbidden")
		}
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	typ := reflect.TypeOf(out)
	if typ == nil || typ.Kind() != reflect.Pointer {
		return fmt.Errorf("pointer required")
	}
	if err := requiredFields(raw, typ.Elem()); err != nil {
		return err
	}
	if v, ok := out.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}
func requiredFields(raw []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			if f.Anonymous && tag == "" {
				if e := requiredFields(raw, f.Type); e != nil {
					return e
				}
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
			b, ok := fields[name]
			if !ok {
				if strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero") {
					continue
				}
				return fmt.Errorf("missing %s", name)
			}
			if e := requiredFields(b, f.Type); e != nil {
				return e
			}
		}
	} else if t.Kind() == reflect.Slice {
		var a []jsontext.Value
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		for _, b := range a {
			if e := requiredFields(b, t.Elem()); e != nil {
				return e
			}
		}
	}
	return nil
}
func Encode(v any) ([]byte, error) { return json.Marshal(v, json.Deterministic(true)) }
func RequestHash(endpoint Endpoint, v any) (string, error) {
	switch endpoint {
	case ResetEndpoint:
		r, ok := v.(ResetRequest)
		if !ok {
			return "", fmt.Errorf("reset DTO required")
		}
		if e := r.Validate(); e != nil {
			return "", e
		}
	case FailureEndpoint:
		r, ok := v.(FailureRequest)
		if !ok {
			return "", fmt.Errorf("failure DTO required")
		}
		if e := r.Validate(); e != nil {
			return "", e
		}
	case OrderEndpoint:
		r, ok := v.(OrderRequest)
		if !ok {
			return "", fmt.Errorf("order DTO required")
		}
		if e := r.Validate(); e != nil {
			return "", e
		}
	default:
		return "", fmt.Errorf("mutation endpoint required")
	}
	return Hash(Version+"/"+string(endpoint), v)
}
