package scenarioexport

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsonBudget measures the plain export DTOs before encoding/json can allocate
// an escaped response. It deliberately rejects types outside this DTO vocabulary.
// No maps, custom marshalers, raw JSON, embedded fields, or floating point values
// occur in export responses.
type jsonBudget struct{ remaining int64 }

func (b *jsonBudget) take(n int64) error {
	if n > b.remaining {
		return ErrTooLarge
	}
	b.remaining -= n
	return nil
}

func (b *jsonBudget) text(s string) error {
	if err := b.take(2); err != nil {
		return err
	}
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		size := int64(n)
		switch {
		case r == utf8.RuneError && n == 1:
			size = 3
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t' || r == '\b' || r == '\f':
			size = 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			size = 6
		}
		if err := b.take(size); err != nil {
			return err
		}
		s = s[n:]
	}
	return nil
}

func (b *jsonBudget) value(v reflect.Value) error {
	if !v.IsValid() {
		return b.take(4)
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return b.take(4)
		}
		return b.value(v.Elem())
	case reflect.String:
		return b.text(v.String())
	case reflect.Bool:
		if v.Bool() {
			return b.take(4)
		}
		return b.take(5)
	case reflect.Int, reflect.Int64:
		return b.take(int64(len(strconv.FormatInt(v.Int(), 10))))
	case reflect.Slice:
		if v.IsNil() {
			return b.take(4)
		}
		if err := b.take(2); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				if err := b.take(1); err != nil {
					return err
				}
			}
			if err := b.value(v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		if err := b.take(2); err != nil {
			return err
		}
		first := true
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" || field.PkgPath != "" {
				continue
			}
			if name == "" || field.Anonymous || (options != "" && options != "omitempty") {
				return fmt.Errorf("unsupported export JSON field %s", field.Name)
			}
			val := v.Field(i)
			if options == "omitempty" && (val.IsZero() || ((val.Kind() == reflect.Slice || val.Kind() == reflect.String) && val.Len() == 0)) {
				continue
			}
			if !first {
				if err := b.take(1); err != nil {
					return err
				}
			}
			first = false
			if err := b.text(name); err != nil {
				return err
			}
			if err := b.take(1); err != nil {
				return err
			}
			if err := b.value(val); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported export JSON type %s", v.Type())
	}
}
