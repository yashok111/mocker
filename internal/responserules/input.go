package responserules

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

func normalizeRequest(ctx context.Context, request Request) (overrides.Input, string, error) {
	if err := ctx.Err(); err != nil {
		return overrides.Input{}, "", err
	}
	if request.Query == nil || len(request.Query) > 100 {
		return overrides.Input{}, "", invalid("/query", "ожидается массив до 100 параметров")
	}
	if request.Headers == nil || len(request.Headers) > 100 {
		return overrides.Input{}, "", invalid("/headers", "ожидается массив до 100 заголовков")
	}
	in := overrides.Input{Query: url.Values{}, Header: http.Header{}}
	for i, f := range request.Query {
		if err := ctx.Err(); err != nil {
			return overrides.Input{}, "", err
		}
		if f.Name == "" || !textBound(f.Name, 256) || !textBound(f.Value, 4096) {
			return in, "", invalid(fmt.Sprintf("/query/%d", i), "недопустимый параметр запроса")
		}
		in.Query.Add(f.Name, f.Value)
	}
	for i, f := range request.Headers {
		if err := ctx.Err(); err != nil {
			return overrides.Input{}, "", err
		}
		if err := checkHeader(f); err != nil {
			return in, "", at(fmt.Sprintf("/headers/%d", i), err)
		}
		in.Header.Add(f.Name, f.Value)
	}
	if request.BodyJSON != nil {
		body, err := decodeBody(ctx, *request.BodyJSON)
		if err != nil {
			return in, "", at("/bodyJSON", err)
		}
		in.Body = body
		in.BodyOK = true
	}
	data, err := jsonx.Marshal(request)
	if err != nil {
		return in, "", err
	}
	if len(data) > MaxFixtureBytes {
		return in, "", invalid("", "пример запроса превышает 128 КиБ")
	}
	if err := ctx.Err(); err != nil {
		return in, "", err
	}
	return in, fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// decodeBody retains number lexemes and rejects duplicates before map decoding.
func decodeBody(ctx context.Context, text string) (any, error) {
	return decodeJSONValue(ctx, text, MaxBodyBytes)
}
func decodeJSONValue(ctx context.Context, text string, limit int) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(text) > limit || !utf8.ValidString(text) {
		return nil, invalid("", "JSON превышает допустимый размер UTF-8")
	}
	s := bodyScanner{ctx: ctx, text: text}
	if err := s.value(0); err != nil {
		return nil, err
	}
	s.space()
	if s.pos != len(text) {
		return nil, invalid("", "лишние данные после JSON")
	}
	decoder := jsonx.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalid("", "ожидается одно значение JSON")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

type bodyScanner struct {
	ctx  context.Context
	text string
	pos  int
}

func (s *bodyScanner) space() {
	for s.pos < len(s.text) && strings.ContainsRune(" \t\r\n", rune(s.text[s.pos])) {
		s.pos++
	}
}
func (s *bodyScanner) quoted() (string, error) {
	start := s.pos
	s.pos++
	for s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		c := s.text[s.pos]
		s.pos++
		if c == '\\' {
			s.pos++
			continue
		}
		if c == '"' {
			var key string
			if err := jsonx.Unmarshal([]byte(s.text[start:s.pos]), &key); err != nil {
				return "", err
			}
			return key, nil
		}
	}
	return "", invalid("", "незавершённая строка JSON")
}
func (s *bodyScanner) value(depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.space()
	if s.pos >= len(s.text) {
		return invalid("", "ожидается JSON")
	}
	switch s.text[s.pos] {
	case '{', '[':
		if depth >= MaxBodyDepth {
			return invalid("", "глубина JSON превышает 64")
		}
		object := s.text[s.pos] == '{'
		end := byte(']')
		if object {
			end = '}'
		}
		s.pos++
		s.space()
		if s.pos < len(s.text) && s.text[s.pos] == end {
			s.pos++
			return nil
		}
		keys := map[string]bool{}
		for {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if object {
				s.space()
				if s.pos >= len(s.text) || s.text[s.pos] != '"' {
					return invalid("", "ожидается ключ JSON")
				}
				key, err := s.quoted()
				if err != nil {
					return err
				}
				if keys[key] {
					return invalid("", "повторяющийся ключ JSON")
				}
				keys[key] = true
				s.space()
				if s.pos >= len(s.text) || s.text[s.pos] != ':' {
					return invalid("", "ожидается двоеточие")
				}
				s.pos++
			}
			if err := s.value(depth + 1); err != nil {
				return err
			}
			s.space()
			if s.pos >= len(s.text) {
				return invalid("", "незавершённый JSON")
			}
			c := s.text[s.pos]
			s.pos++
			if c == end {
				return nil
			}
			if c != ',' {
				return invalid("", "ожидается запятая")
			}
		}
	case '"':
		_, err := s.quoted()
		return err
	default:
		start := s.pos
		for s.pos < len(s.text) && !strings.ContainsRune(",]} \t\r\n", rune(s.text[s.pos])) {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			s.pos++
		}
		if s.pos == start {
			return invalid("", "ожидается значение JSON")
		}
		return nil
	}
}
