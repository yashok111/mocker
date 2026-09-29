// Private numeric tokens preserve JSON lexemes without exposing object fields
// that a JSON Pointer could accidentally traverse. All helpers are isolated
// from the collection's ordinary assertion/extraction JSON implementation.
const applyPostmanBindings = (() => {
  const numbers = new WeakMap();
  const caseRanges = /*GO_CASE_RANGES*/[];
  const maxDepth = 10000;
  const maxBody = 1 << 20;
  const own = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
  const trim = value => value.replace(/^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g, '');
  const numberPattern = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?/;
  const integerPattern = /^-?(0|[1-9][0-9]*)/;
  function number(raw) {
    const token = Object.create(null);
    numbers.set(token, raw);
    return token;
  }
  function object(value) {
    return value !== null && typeof value === 'object' && !Array.isArray(value) && !numbers.has(value);
  }
  function scalarString(value) {
    let out = '';
    for (let i = 0; i < value.length; i++) {
      const code = value.charCodeAt(i);
      if (code >= 0xd800 && code <= 0xdbff) {
        const next = value.charCodeAt(i + 1);
        if (next >= 0xdc00 && next <= 0xdfff) { out += value[i] + value[++i]; continue; }
        out += '\ufffd';
      } else { out += code >= 0xdc00 && code <= 0xdfff ? '\ufffd' : value[i]; }
    }
    return out;
  }
  function parse(raw) {
    let offset = 0;
    const stack = [];
    const invalid = () => { throw new Error('invalid JSON'); };
    const whitespace = () => { while (offset < raw.length && /[\x20\t\r\n]/.test(raw[offset])) offset++; };
    function string() {
      const start = offset++;
      while (offset < raw.length) {
        const char = raw[offset++];
        if (char === '"') return scalarString(JSON.parse(raw.slice(start, offset)));
        if (char === '\\') offset++;
      }
      return invalid();
    }
    function value() {
      whitespace();
      const char = raw[offset];
      if (char === '"') return string();
      if (char === '[' || char === '{') {
        if (stack.length >= maxDepth) throw new Error('JSON nesting exceeds 10000 levels');
        const result = char === '[' ? [] : Object.create(null);
        offset++;
        stack.push({value: result, array: char === '[', state: 'first'});
        return result;
      }
      for (const [text, result] of [['true', true], ['false', false], ['null', null]]) {
        if (raw.startsWith(text, offset)) { offset += text.length; return result; }
      }
      const match = raw.slice(offset).match(numberPattern);
      if (!match) return invalid();
      offset += match[0].length;
      return number(match[0]);
    }
    const root = value();
    while (stack.length) {
      const frame = stack[stack.length - 1];
      const close = frame.array ? ']' : '}';
      whitespace();
      if ((frame.state === 'first' || frame.state === 'separator') && raw[offset] === close) {
        offset++;
        stack.pop();
        continue;
      }
      if (frame.state === 'separator') {
        if (raw[offset++] !== ',') return invalid();
        frame.state = 'next';
        continue;
      }
      if (frame.array) { frame.value.push(value()); }
      else {
        if (raw[offset] !== '"') return invalid();
        const key = string();
        whitespace();
        if (raw[offset++] !== ':') return invalid();
        frame.value[key] = value();
      }
      frame.state = 'separator';
    }
    whitespace();
    if (offset !== raw.length) return invalid();
    return root;
  }
  function quote(value) {
    return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, char => '\\u' + char.charCodeAt(0).toString(16).padStart(4, '0'));
  }
  function compareKeys(left, right) {
    let a = 0, b = 0;
    while (a < left.length && b < right.length) {
      const x = left.codePointAt(a), y = right.codePointAt(b);
      if (x !== y) return x - y;
      a += x > 0xffff ? 2 : 1;
      b += y > 0xffff ? 2 : 1;
    }
    return (a < left.length ? 1 : 0) - (b < right.length ? 1 : 0);
  }
  function stringify(root) {
    // An explicit stack also handles the Go decoder's full nesting limit.
    const out = [], stack = [{value: root, depth: 0}];
    while (stack.length) {
      const item = stack.pop(), value = item.value;
      if (item.text !== undefined) { out.push(item.text); continue; }
      if (numbers.has(value)) { out.push(numbers.get(value)); continue; }
      if (value === null || typeof value === 'boolean') { out.push(String(value)); continue; }
      if (typeof value === 'string') { out.push(quote(value)); continue; }
      if (item.depth >= maxDepth) throw new Error('JSON nesting exceeds 10000 levels');
      const array = Array.isArray(value), keys = array ? null : Object.keys(value).sort(compareKeys);
      out.push(array ? '[' : '{');
      stack.push({text: array ? ']' : '}'});
      const length = array ? value.length : keys.length;
      for (let i = length - 1; i >= 0; i--) {
        stack.push({value: array ? value[i] : value[keys[i]], depth: item.depth + 1});
        if (!array) stack.push({text: quote(keys[i]) + ':'});
        if (i > 0) stack.push({text: ','});
      }
    }
    return out.join('');
  }
  function utf8Length(text) {
    let size = 0;
    for (const char of text) {
      const code = char.codePointAt(0);
      size += code < 0x80 ? 1 : code < 0x800 ? 2 : code < 0x10000 ? 3 : 4;
    }
    return size;
  }
  function isInteger(raw) {
    const parts = raw.toLowerCase().split('e');
    const coefficient = parts[0].replace(/^-/, '');
    const dot = coefficient.indexOf('.');
    const digits = coefficient.replace('.', '').replace(/^0+/, '');
    if (!digits) return true;
    const shift = (digits.match(/0*$/)[0].length) - (dot < 0 ? 0 : coefficient.length - dot - 1);
    const exponent = parts[1] || '0';
    const magnitude = exponent.replace(/^[+-]/, '').replace(/^0+/, '') || '0';
    const negative = exponent[0] === '-' && magnitude !== '0';
    if (negative && shift < 0) return false;
    if (!negative && shift >= 0) return true;
    const bound = String(Math.abs(shift));
    const comparison = magnitude.length - bound.length || (magnitude < bound ? -1 : magnitude > bound ? 1 : 0);
    return negative ? comparison <= 0 : comparison >= 0;
  }
  function type(value) {
    if (value === null) return 'null';
    if (numbers.has(value)) return isInteger(numbers.get(value)) ? 'integer' : 'number';
    if (Array.isArray(value)) return 'array';
    return typeof value;
  }
  function validPointer(pointer) {
    return typeof pointer === 'string' && [...pointer].length <= 2000 &&
      (pointer === '' || pointer[0] === '/') && !/~(?:[^01]|$)/.test(pointer);
  }
  const unescape = token => token.replace(/~1/g, '/').replace(/~0/g, '~');
  function arrayIndex(value, token) {
    return /^(0|[1-9][0-9]*)$/.test(token) && Number.isSafeInteger(Number(token)) && Number(token) < value.length;
  }
  function read(root, pointer) {
    if (!validPointer(pointer)) return {found: false};
    let value = root;
    for (const token of pointer === '' ? [] : pointer.slice(1).split('/').map(unescape)) {
      if (Array.isArray(value) ? !arrayIndex(value, token) : !object(value) || !own(value, token)) return {found: false};
      value = value[token];
    }
    return {found: true, value};
  }
  function write(root, pointer, value) {
    if (!validPointer(pointer)) throw new Error('invalid target JSON Pointer');
    if (pointer === '') return value;
    const slash = pointer.lastIndexOf('/');
    const parent = read(root, pointer.slice(0, slash)), token = unescape(pointer.slice(slash + 1));
    if (!parent.found) throw new Error('target intermediate container is missing');
    if (Array.isArray(parent.value)) {
      if (!arrayIndex(parent.value, token)) throw new Error('target array index is missing');
    } else if (!object(parent.value)) { throw new Error('target parent must be an object or array'); }
    parent.value[token] = value;
    return root;
  }
  function changeCase(value, upper) {
    let result = '';
    for (const char of value) {
      const code = char.codePointAt(0);
      let mapped = code, lo = 0, hi = caseRanges.length;
      while (lo < hi) {
        const mid = (lo + hi) >> 1, span = caseRanges[mid];
        if (code < span[0]) hi = mid;
        else if (code > span[1]) lo = mid + 1;
        else {
          const delta = span[upper ? 2 : 3];
          mapped = delta > 0x10ffff ? span[0] + (((code - span[0]) & ~1) | (upper ? 0 : 1)) : code + delta;
          break;
        }
      }
      result += String.fromCodePoint(mapped);
    }
    return result;
  }
  function transform(value, actual, transforms) {
    if (transforms.length > 8) throw new Error('допускается не более 8 преобразований');
    for (let i = 0; i < transforms.length; i++) {
      const kind = transforms[i].kind;
      const accepts = kind === 'to_string' ? ['string', 'integer', 'number', 'boolean'].includes(actual) :
        ['trim', 'lower', 'upper', 'to_number', 'to_integer'].includes(kind) && actual === 'string';
      if (!accepts) throw new Error('преобразование ' + (i + 1) + ' (' + kind + ') не принимает тип ' + actual);
      if (kind === 'trim') value = trim(value);
      else if (kind === 'lower' || kind === 'upper') value = changeCase(value, kind === 'upper');
      else if (kind === 'to_string') value = numbers.has(value) ? numbers.get(value) : String(value);
      else {
        const match = value.match(kind === 'to_integer' ? integerPattern : numberPattern);
        if (!match || match[0] !== value) throw new Error('преобразование ' + (i + 1) + ' (' + kind + '): некорректное число JSON');
        value = number(value);
      }
      actual = kind === 'to_integer' ? 'integer' : kind === 'to_number' ? 'number' : 'string';
      if (utf8Length(stringify(value)) > maxBody) throw new Error('результат преобразования превышает допустимый размер');
    }
    return {value, actual};
  }
  const compatible = (actual, want) => actual === want || want === 'unknown' || actual === 'unknown' || (actual === 'integer' && want === 'number');
  function safeText(value) {
    if (value.includes('{{') || value.includes('\u0000')) throw new Error('Unsupported template or NUL in binding value');
  }
  function decode(raw, reason) {
    try { return parse(raw); }
    catch (error) { throw new Error(reason + (error.message.includes('nesting') ? ': ' + error.message : '')); }
  }
  return function applyPostmanBindings(source, responses) {
    let body = null, bodyDecoded = false, lastBodyBinding;
    for (const binding of source.bindings || []) {
      try {
        if (!own(responses, binding.sourceMessageId) || typeof responses[binding.sourceMessageId] !== 'string') {
          throw new Error('successful source occurrence is unavailable');
        }
        const parsed = decode(responses[binding.sourceMessageId], 'source response is not valid JSON');
        const selected = read(parsed, binding.sourcePointer);
        if (!selected.found) throw new Error('source JSON Pointer is missing');
        const transformed = transform(selected.value, type(selected.value), binding.transforms || []);
        const value = transformed.value, actual = transformed.actual;
        const want = source.bindingTypes && own(source.bindingTypes, binding.id) ? source.bindingTypes[binding.id] : 'unknown';
        const target = binding.target;
        if (target.kind === 'body') {
          if (!compatible(actual, want)) throw new Error('incompatible binding types: ' + actual + ' → ' + want);
          if (!bodyDecoded) {
            if (target.pointer !== '' || trim(source.body) !== '') body = decode(source.body, 'request body is not valid JSON');
            bodyDecoded = true;
          }
          body = write(body, target.pointer, value);
          lastBodyBinding = binding.id;
        } else {
          if (['null', 'object', 'array'].includes(actual)) throw new Error('text targets require a non-null scalar');
          if (binding.prefix && want !== 'unknown' && want !== 'string') throw new Error('prefix requires a string target');
          if (want !== 'string' && !compatible(actual, want)) throw new Error('incompatible binding types: ' + actual + ' → ' + want);
          const text = (binding.prefix || '') + (typeof value === 'string' ? value : stringify(value));
          if ([...text].length > 50000) throw new Error('resolved parameter is too large');
          safeText(text);
          if (target.kind === 'header' && /[\x00-\x08\x0a-\x1f\x7f]/.test(text)) throw new Error('Invalid HTTP header');
          const field = {path: 'pathParams', query: 'query', header: 'headers'}[target.kind];
          if (!field || !own({path: 1, query: 1, header: 1}, target.kind)) throw new Error('invalid target kind');
          if (!source[field]) source[field] = Object.create(null);
          Object.defineProperty(source[field], target.name, {value: text, enumerable: true, writable: true, configurable: true});
        }
      } catch (error) { throw new Error('binding ' + quote(binding.id) + ': ' + error.message); }
    }
    if (bodyDecoded) {
      try {
        const encoded = stringify(body);
        if (utf8Length(encoded) > maxBody) throw new Error('resolved body exceeds 1 MiB');
        safeText(encoded);
        source.body = encoded;
      } catch (error) { throw new Error('binding ' + quote(lastBodyBinding) + ': ' + error.message); }
    }
  };
})();
