package recordproxy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/probe"
)

// RequestKey stores no request material. Credentials participate to prevent
// one caller replaying another caller's private response.
func RequestKey(upstream string, r *http.Request, body []byte) string {
	var value any
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json") && jsonx.Valid(body) {
		dec := jsonx.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if dec.Decode(&value) == nil {
			if b, err := jsonx.Marshal(value); err == nil {
				body = b
			}
		}
	}
	parts := make([]string, 0, 6)
	parts = append(parts, strings.TrimRight(upstream, "/"), r.Method, r.URL.EscapedPath(), r.URL.Query().Encode(), string(body))
	// Hash every end-to-end request header: tenant selectors, conditional
	// requests and range headers can all change the upstream response.
	headers := map[string][]string{}
	for name, values := range probe.ProxyRequestHeaders(r.Header, true, true) {
		headers[strings.ToLower(name)] = values
	}
	encodedHeaders, _ := jsonx.Marshal(headers)
	parts = append(parts, string(encodedHeaders))

	raw, _ := jsonx.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
