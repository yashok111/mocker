// Execute generated collection scripts at the Postman API boundary. Real HTTP
// and sandbox compatibility are additionally checked with Newman during QA.
const fs = require('node:fs');
const assert = require('node:assert/strict');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const collection = JSON.parse(input.artifact);
const variables = new Map(collection.variable.map(v => [v.key, v.value]));
const local = new Map();
const output = {requests: [], errors: [], skipped: false};
const expected = value => ({to: {equal: other => assert.equal(value, other), deep: {equal: other => assert.deepEqual(value, other)}, be: {within: (low, high) => assert.ok(value >= low && value <= high)}}});
const scope = map => ({get: name => map.get(name), set: (name, value) => map.set(name, value), unset: name => map.delete(name), has: name => map.has(name)});
const order = input.order || collection.item.map((_, i) => i);
let responseIndex = 0;
for (let iteration = 0; iteration < (input.iterations || 1); iteration++) {
  let stopped = false;
  for (const index of order) {
    if (stopped) break;
    const item = collection.item[index];
    const request = {index, iteration, url: item.request.url, headers: {}, body: item.request.body?.raw || ''};
    let skipped = false;
    const pm = {
      info: {iteration, requestId: item.id, requestName: item.name},
      variables: scope(local), collectionVariables: scope(variables),
      request: {
        url: {update: value => request.url = value},
        headers: {upsert: ({key, value}) => { for (const existing of Object.keys(request.headers)) if(existing.toLowerCase() === key.toLowerCase()) delete request.headers[existing]; request.headers[key] = value; }},
        body: item.request.body ? {update: value => request.body = value} : undefined,
      },
      response: {code: input.codes?.[responseIndex] || 201, text: () => input.responses?.[responseIndex] || '{}'},
      execution: {skipRequest: () => {skipped = true; output.skipped = true;}, setNextRequest: value => {stopped = value === null;}},
      test: (_, callback) => {try {callback();} catch(error) {output.errors.push(error.message);}}, expect: expected,
    };
    try {
      for (const event of item.event.filter(e => e.listen === 'prerequest')) new Function('pm', event.script.exec.join('\n'))(pm);
      if (!skipped) {
        output.requests.push(request);
        for (const event of item.event.filter(e => e.listen === 'test')) new Function('pm', event.script.exec.join('\n'))(pm);
      }
    } catch(error) {output.errors.push(error.message); stopped = true;}
    responseIndex++;
  }
  if (output.errors.length) break;
}
output.local = Object.fromEntries(local);
output.variables = Object.fromEntries(variables);
process.stdout.write(JSON.stringify(output));
