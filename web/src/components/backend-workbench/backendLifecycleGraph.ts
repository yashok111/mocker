import type {
  BackendLifecyclePayload,
  BackendDiagramViewState,
  StateDiagram,
} from "@/api/generated/schemas";
// Presentation only: never pass this adapter to an editor writer or simulator.
export function lifecycleGraph(
  payload: BackendLifecyclePayload,
  view: Pick<BackendDiagramViewState, "positions" | "collapsedIds">,
) {
  const hidden = new Set(view.collapsedIds);
  const states = payload.states.filter((s) => !hidden.has(s.id)).slice(0, 200);
  const ids = new Set(states.map((s) => s.id));
  const transitions = payload.transitions
    .filter((t) => ids.has(t.from) && ids.has(t.to))
    .slice(0, 600);
  const diagram: StateDiagram = {
    id: "backend-lifecycle",
    name: "Жизненный цикл",
    initialStateId: states.find((s) => s.initial)?.id ?? "",
    states: states.map((s, i) => {
      const at = view.positions.find((p) => p.id === s.id);
      return {
        id: s.id,
        name: s.label,
        x: at?.x ?? 60 + (i % 4) * 280,
        y: at?.y ?? 80 + Math.floor(i / 4) * 200,
        terminal: s.terminal,
      };
    }),
    transitions: transitions.map((t) => ({
      id: t.id,
      name: t.label + (t.guard.kind === "opaque" ? " · условие не проверено" : ""),
      from: t.from,
      to: t.to,
      patchJSON: "{}",
      responseStatus: 200,
    })),
  };
  return {
    diagram,
    limited:
      states.length !== payload.states.length || transitions.length !== payload.transitions.length,
  };
}
