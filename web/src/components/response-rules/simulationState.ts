import type { ResponseRuleRequest } from "@/api/generated/schemas";
export function evaluationIdentity(input: {
  designId: number;
  ruleId: string;
  document: string;
  generation: string;
  request: ResponseRuleRequest;
}): string {
  return JSON.stringify(input);
}
// Abort is an optimization. Tokens independently reject obsolete completions,
// including an A → B → A edit sequence and a transport that ignores abort.
export function createEvaluationGate() {
  let identity = "";
  let generation = 0;
  return {
    update(next: string) {
      if (next !== identity) {
        identity = next;
        generation++;
      }
    },
    begin() {
      return { identity, generation: ++generation };
    },
    accept(token: { identity: string; generation: number }) {
      return token.identity === identity && token.generation === generation;
    },
    dispose() {
      generation++;
    },
  };
}
