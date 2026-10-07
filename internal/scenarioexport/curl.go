package scenarioexport

import (
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// exportText remembers the first budget error so subsequent formatting writes
// cannot grow the artifact. WriteString must not bypass boundedBuffer.Write.
type exportText struct {
	buffer boundedBuffer
	err    error
}

func (w *exportText) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.buffer.Write(data)
	w.err = err
	return n, err
}
func (w *exportText) WriteString(value string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(value)) > w.buffer.limit-int64(w.buffer.Len()) {
		w.err = ErrTooLarge
		return 0, w.err
	}
	return w.Write([]byte(value))
}

// writeShellWord streams a single POSIX shell word without creating an
// unbounded escaped copy. No user text is shell source.
func writeShellWord(out io.StringWriter, value string) {
	_, _ = out.WriteString("'")
	first := true
	for part := range strings.SplitSeq(value, "'") {
		if !first {
			_, _ = out.WriteString("'\"'\"'")
		}
		first = false
		_, _ = out.WriteString(part)
	}
	_, _ = out.WriteString("'")
}

func (s *Service) renderCURL(prepared httpExport) ([]byte, error) {
	out := exportText{buffer: boundedBuffer{limit: s.maxBytes}}
	_, _ = out.WriteString("#!/bin/sh\nset -eu\n\n# Edit the saved service URLs before running. Response bodies are discarded.\n")
	for _, base := range prepared.Bases {
		_, _ = fmt.Fprintf(&out, "%s=", base.Name)
		writeShellWord(&out, base.URL)
		_, _ = fmt.Fprintf(&out, "\n: \"${%s:?Fill in %s before running}\"\n", base.Name, base.Name)
	}
	for i, request := range prepared.Requests {
		if out.err != nil {
			return nil, out.err
		}
		// Percent encoding expands each byte by at most three. Bound temporary URL
		// construction as well as the final shell output before allocating it.
		urlBytes := int64(len(request.Path))
		for _, value := range request.Execution.PathParams {
			urlBytes += 3 * int64(len(value))
		}
		for key, value := range request.Execution.Query {
			urlBytes += 3*int64(len(key)+len(value)) + 2
		}
		if urlBytes > s.maxBytes {
			return nil, ErrTooLarge
		}
		path := concretePath(request.Path, request.Execution.PathParams)
		query := url.Values{}
		for key, value := range request.Execution.Query {
			query.Set(key, value)
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		_, _ = fmt.Fprintf(&out, "\n# Request %d\nstatus=$(", i+1)
		if request.Execution.Body != "" {
			_, _ = out.WriteString("printf '%s' ")
			writeShellWord(&out, request.Execution.Body)
			_, _ = out.WriteString(" | ")
		}
		_, _ = out.WriteString("curl --disable --silent --show-error --globoff --path-as-is --proto '=http,https' --max-time 30 --output /dev/null --write-out '%{http_code}' --request ")
		writeShellWord(&out, request.Method)
		for _, key := range slices.Sorted(maps.Keys(request.Execution.Headers)) {
			value := request.Execution.Headers[key]
			header := key + ": " + value
			if value == "" {
				header = key + ";"
			}
			_, _ = out.WriteString(" --header ")
			writeShellWord(&out, header)
		}
		if request.Execution.Body != "" {
			_, _ = out.WriteString(" --data-binary @-")
		}
		_, _ = fmt.Fprintf(&out, " --url \"${%s}\"", request.Base)
		writeShellWord(&out, path)
		_, _ = out.WriteString(")\nprintf 'HTTP %s\\n' \"$status\"\n")
		if request.Execution.ExpectedStatus != nil {
			_, _ = fmt.Fprintf(&out, "[ \"$status\" = '%d' ] || { printf 'Unexpected HTTP status\\n' >&2; exit 1; }\n", *request.Execution.ExpectedStatus)
		} else {
			_, _ = out.WriteString("case \"$status\" in 2??) ;; *) printf 'Expected HTTP 2xx\\n' >&2; exit 1 ;; esac\n")
		}
	}
	if out.err != nil {
		return nil, out.err
	}
	return out.buffer.Bytes(), nil
}
