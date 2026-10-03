import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { BackendFlowInspector } from "./BackendFlowInspector";
import { BackendDatabaseInspector } from "./BackendDatabaseInspector";
import type { FlowSelection } from "./backendFlowReads";
import { usePinnedValue } from "./backendFlowReads";
import { lineageRefKey } from "./backendLineageReads";
import { BackendEventValueInspector } from "./BackendEventValueInspector";
// The exact value is transient inspector state; it does not alter saved-view-v1.
export function BackendValueInspector({
  projectId,
  revisionId,
  value,
  onClose,
}: {
  projectId: string;
  revisionId: string;
  value: BackendLineageValueRef;
  onClose: () => void;
}) {
  const [selection, setSelection] = usePinnedValue<FlowSelection>(
    `${projectId}:${revisionId}:${lineageRefKey(value)}`,
    {
      type: "node",
      id: value.nodeId,
      valueRef: value,
    },
  );
  const depart = useBackendAPIDeparture();
  const select = (next: FlowSelection) => {
    depart(() => setSelection(next));
  };
  const close = () => {
    depart(onClose);
  };
  return selection.valueRef?.kind === "event_field" ? (
    <BackendEventValueInspector
      key={lineageRefKey(selection.valueRef)}
      projectId={projectId}
      revisionId={revisionId}
      value={selection.valueRef}
      onValueSelect={(next) => select({ type: "node", id: next.nodeId, valueRef: next })}
      onClose={close}
    />
  ) : selection.valueRef?.kind === "column" ? (
    <BackendDatabaseInspector
      context={{ projectId, revisionId, datastoreId: "", facetKey: selection.valueRef.facetKey }}
      valueRef={selection.valueRef}
      selection={selection}
      onSelect={select}
      onClose={close}
    />
  ) : (
    <BackendFlowInspector
      key={JSON.stringify(selection)}
      projectId={projectId}
      revisionId={revisionId}
      selection={selection}
      selectedValue={selection.valueRef}
      onSelect={select}
      onClose={close}
    />
  );
}
