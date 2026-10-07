package scenarioexport

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

// All data embedded in JavaScript passes through JSON encoding. These scripts
// never evaluate source strings and never recursively interpolate variables.
const postmanPrepare = `
function substitute(source) {
  const result = source.replace(/\{\{([^{}]+)\}\}/g, (_, name) => {
    const value = pm.collectionVariables.get(name);
    if (value === undefined || value === null) throw new Error('Missing variable: ' + name);
    return String(value);
  });
  if (result.includes('{{') || result.includes('\u0000')) throw new Error('Unsupported template or NUL in input');
  return result;
}
function escapePart(value) {
  return encodeURIComponent(value).replace(/[!'()*]/g, c => '%' + c.charCodeAt(0).toString(16).toUpperCase());
}
try {
  if (source.flow) beginBindingStep(source.flow);
  // Substitute authored inputs once, before response values are written.
  for (const values of [source.pathParams, source.query, source.headers]) {
    for (const key of Object.keys(values)) values[key] = substitute(values[key]);
  }
  source.body = substitute(source.body);
  if (source.bindings && source.bindings.length) {
    applyPostmanBindings(source, bindingState.responses);
  }
  if (source.flow) {
    for (const id of source.flow.release) delete bindingState.responses[id];
    pm.variables.set(source.flow.key, JSON.stringify(bindingState));
  }
  // Postman's later interpolation must never reinterpret response data.
  for (const values of [source.pathParams, source.query, source.headers]) {
    for (const value of Object.values(values)) {
      if (value.includes('{{') || value.includes('\u0000')) throw new Error('Unsupported template or NUL in binding input');
    }
  }
  if (source.body.includes('{{') || source.body.includes('\u0000')) throw new Error('Unsupported template or NUL in body');
  const base = pm.collectionVariables.get(source.base);
  if (typeof base !== 'string' || !/^https?:\/\/[^/?#@\s{}]+(?:\/[^?#\s{}]*)?$/.test(base)) {
    throw new Error('Fill in a valid HTTP(S) URL: ' + source.base);
  }
  const path = source.path.replace(/\{([^{}]+)\}/g, (_, key) => escapePart(source.pathParams[key]));
  const query = Object.keys(source.query).sort().map(key => escapePart(key) + '=' + escapePart(source.query[key])).join('&');
  pm.request.url.update(base.replace(/\/+$/, '') + path + (query ? '?' + query : ''));
  for (const key of Object.keys(source.headers)) {
    const value = source.headers[key];
    if (/[\x00-\x08\x0a-\x1f\x7f]/.test(value)) throw new Error('Invalid HTTP header');
    pm.request.headers.upsert({key, value});
  }
  if (source.hasBody) pm.request.body.update(source.body);
} catch (error) {
  if (source.flow) pm.variables.unset(source.flow.key);
  pm.execution.skipRequest();
  pm.execution.setNextRequest(null);
  throw error;
}
`

// Local variables live only for a collection run. Sequence and iteration checks
// prevent using a source from an earlier iteration or a skipped/failed request.
const postmanBindingState = `
let bindingState;
function bindingBytes(text) { return encodeURIComponent(text).replace(/%[0-9A-F]{2}/g, 'x').length; }
function beginBindingStep(flow) {
  const iteration = pm.info.iteration;
  if (flow.index === 0) {
    bindingState = {iteration, next: 0, responses: {}};
  } else {
    const saved = pm.variables.get(flow.key);
    if (typeof saved !== 'string') throw new Error('Binding source unavailable: run the complete collection in order');
    bindingState = JSON.parse(saved);
  }
  if (bindingState.iteration !== iteration || bindingState.next !== flow.index) {
    throw new Error('Binding source unavailable: a previous step was skipped or failed');
  }
  bindingState.next = -1;
  pm.variables.set(flow.key, JSON.stringify(bindingState));
}
function completeBindingStep(flow) {
  const state = JSON.parse(pm.variables.get(flow.key));
  if (state.iteration !== pm.info.iteration || state.next !== -1) throw new Error('Invalid binding run state');
  if (flow.capture) {
    const response = pm.response.text();
    if (bindingBytes(response) > 1048576) throw new Error('Binding source response exceeds 1 MiB');
    Object.defineProperty(state.responses, flow.messageId, {value: response, enumerable: true, configurable: true, writable: true});
  }
  if (flow.last) { pm.variables.unset(flow.key); return; }
  state.next = flow.index + 1;
  const saved = JSON.stringify(state);
  if (bindingBytes(saved) > 20971520) throw new Error('Binding response cache exceeds 20 MiB');
  pm.variables.set(flow.key, saved);
}
`

type postmanFlow struct {
	Key       string   `json:"key"`
	MessageID string   `json:"messageId"`
	Index     int      `json:"index"`
	Capture   bool     `json:"capture"`
	Last      bool     `json:"last"`
	Release   []string `json:"release"`
}

func postmanFlows(prepared httpExport) []*postmanFlow {
	lastUse := map[string]int{}
	reserved := map[string]bool{}
	for name := range prepared.Variables {
		reserved[name] = true
	}
	for _, base := range prepared.Bases {
		reserved[base.Name] = true
	}
	for i, request := range prepared.Requests {
		for _, extraction := range request.Execution.Extract {
			reserved[extraction.Name] = true
		}
		for _, binding := range request.Execution.Bindings {
			lastUse[binding.SourceMessageID] = i
		}
	}
	flows := make([]*postmanFlow, len(prepared.Requests))
	if len(lastUse) == 0 {
		return flows
	}
	key := "__MOCKER_BINDING_STATE"
	for reserved[key] {
		key += "_"
	}
	for i, request := range prepared.Requests {
		release := []string{}
		for _, id := range slices.Sorted(maps.Keys(lastUse)) {
			if lastUse[id] == i {
				release = append(release, id)
			}
		}
		_, capture := lastUse[request.MessageID]
		flows[i] = &postmanFlow{Key: key, MessageID: request.MessageID, Index: i, Capture: capture, Last: i == len(prepared.Requests)-1, Release: release}
	}
	return flows
}

func (s *Service) renderPostman(title string, prepared httpExport) ([]byte, error) {
	variables := make([]any, 0, len(prepared.Variables)+len(prepared.Bases))
	for _, name := range slices.Sorted(maps.Keys(prepared.Variables)) {
		variables = append(variables, map[string]any{"key": name, "value": prepared.Variables[name], "type": "string"})
	}
	for _, base := range prepared.Bases {
		variables = append(variables, map[string]any{"key": base.Name, "value": base.URL, "type": "string"})
	}
	items := []any{}
	flows := postmanFlows(prepared)
	for index, request := range prepared.Requests {
		headers := []any{}
		for _, key := range slices.Sorted(maps.Keys(request.Execution.Headers)) {
			headers = append(headers, map[string]any{"key": key, "value": request.Execution.Headers[key]})
		}
		rawURL := "{{" + request.Base + "}}" + httpPathParameter.ReplaceAllStringFunc(request.Path, func(token string) string { return request.Execution.PathParams[token[1:len(token)-1]] })
		wire := map[string]any{"method": request.Method, "url": rawURL, "header": headers, "auth": map[string]any{"type": "noauth"}}
		hasBodyBinding := slices.ContainsFunc(request.Execution.Bindings, func(b designscenario.DataBinding) bool { return b.Target.Kind == "body" })
		hasBody := request.Execution.Body != "" || hasBodyBinding
		if hasBody {
			wire["body"] = map[string]any{"mode": "raw", "raw": request.Execution.Body}
		}
		source := map[string]any{"base": request.Base, "path": request.Path, "pathParams": request.Execution.PathParams, "query": request.Execution.Query, "headers": request.Execution.Headers, "body": request.Execution.Body}
		source["hasBody"] = hasBody
		flow := flows[index]
		if flow != nil {
			source["flow"] = flow
		}
		if len(request.Execution.Bindings) > 0 {
			source["bindings"], source["bindingTypes"] = request.Execution.Bindings, request.BindingTypes
		}
		if err := s.CheckResponse(source); err != nil {
			return nil, err
		}
		data, err := jsonx.Marshal(source)
		if err != nil {
			return nil, err
		}
		if err := s.CheckResponse(string(data)); err != nil {
			return nil, err
		}
		quoted, err := jsonx.Marshal(string(data))
		if err != nil {
			return nil, err
		}
		runtime := ""
		if flow != nil {
			runtime = postmanBindingState
		}
		if len(request.Execution.Bindings) > 0 {
			runtime += postmanBindingRuntime(request.Execution.Bindings)
		}
		if int64(len(quoted)+len(runtime)+len(postmanPrepare)+40) > s.maxBytes {
			return nil, ErrTooLarge
		}
		prepare := "const source = JSON.parse(" + string(quoted) + ");\n" + runtime + postmanPrepare
		test, err := s.postmanTestScript(request, flow)
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"name": request.Name, "request": wire,
			"protocolProfileBehavior": map[string]any{"disableUrlEncoding": true},
			"event":                   []any{postmanEvent("prerequest", prepare), postmanEvent("test", test)},
		})
		if err := s.CheckResponse(items); err != nil {
			return nil, err
		}
	}
	collection := map[string]any{
		"info": map[string]any{"name": title, "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
		"item": items, "variable": variables,
	}
	if err := s.CheckResponse(collection); err != nil {
		return nil, err
	}
	buffer := boundedBuffer{limit: s.maxBytes}
	if err := jsonx.NewEncoder(&buffer).Encode(collection); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func postmanEvent(listen, script string) map[string]any {
	return map[string]any{"listen": listen, "script": map[string]any{"type": "text/javascript", "exec": strings.Split(script, "\n")}}
}

func (s *Service) postmanTestScript(request httpRequest, flow *postmanFlow) (string, error) {
	out := exportText{buffer: boundedBuffer{limit: s.maxBytes}}
	if flow != nil {
		data, err := jsonx.Marshal(flow)
		if err != nil {
			return "", err
		}
		_, _ = out.WriteString("const flow = " + string(data) + ";\n" + postmanBindingState)
	}
	hasJSON := len(request.Execution.Assertions)+len(request.Execution.Extract) > 0
	if hasJSON {
		_, _ = out.WriteString(postmanBindingRuntime(nil))
	}
	// pm.test captures assertion errors instead of throwing them to the script.
	// Stop the collection inside its callback, before that error is swallowed.
	_, _ = out.WriteString("pm.test('Scenario step', () => {\ntry {\n")
	if request.Execution.ExpectedStatus != nil {
		_, _ = fmt.Fprintf(&out, "pm.expect(pm.response.code).to.equal(%d);\n", *request.Execution.ExpectedStatus)
	} else {
		_, _ = out.WriteString("pm.expect(pm.response.code).to.be.within(200, 299);\n")
	}
	if hasJSON {
		_, _ = out.WriteString("const response = postmanJSONRuntime.parse(pm.response.text());\n")
	}
	for _, assertion := range request.Execution.Assertions {
		if err := s.CheckResponse([]string{assertion.Pointer, string(assertion.Equals)}); err != nil {
			return "", err
		}
		pointer, _ := jsonx.Marshal(assertion.Pointer)
		expected, _ := jsonx.Marshal(string(assertion.Equals))
		_, _ = fmt.Fprintf(&out, "pm.expect(postmanJSONRuntime.equal(postmanJSONRuntime.readPointer(response, %s), postmanJSONRuntime.parse(%s))).to.equal(true);\n", pointer, expected)
	}
	if len(request.Execution.Extract) > 0 {
		// Match the runner's atomic variable update: a missing later pointer must
		// not publish earlier extractions from the same response.
		_, _ = out.WriteString("const extracted = [];\n")
		for _, extraction := range request.Execution.Extract {
			if err := s.CheckResponse([]string{extraction.Name, extraction.Pointer}); err != nil {
				return "", err
			}
			name, _ := jsonx.Marshal(extraction.Name)
			pointer, _ := jsonx.Marshal(extraction.Pointer)
			_, _ = fmt.Fprintf(&out, "{ const value = postmanJSONRuntime.readPointer(response, %s); extracted.push([%s, typeof value === 'string' ? value : postmanJSONRuntime.stringify(value)]); }\n", pointer, name)
		}
	}
	if flow != nil {
		_, _ = out.WriteString("completeBindingStep(flow);\n")
	}
	if len(request.Execution.Extract) > 0 {
		_, _ = out.WriteString("for (const [name, value] of extracted) pm.collectionVariables.set(name, value);\n")
	}
	_, _ = out.WriteString("} catch (error) {\n")
	if flow != nil {
		_, _ = out.WriteString("pm.variables.unset(flow.key);\n")
	}
	_, _ = out.WriteString("pm.execution.setNextRequest(null);\nthrow error;\n}\n});\n")
	return out.buffer.String(), out.err
}
