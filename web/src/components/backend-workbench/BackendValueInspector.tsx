import { useState } from "react";
import { useBackendAPIDeparture } from "./useBackendAPIDeparture";
import type { BackendLineageValueRef } from "@/api/generated/schemas";
import { BackendFlowInspector } from "./BackendFlowInspector";
import { BackendDatabaseInspector } from "./BackendDatabaseInspector";
import type { FlowSelection } from "./backendFlowReads";
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
  const [selection, setSelection] = useState<FlowSelection>({
    type: "node",
    id: value.nodeId,
    valueRef: value,
  });
  const depart = useBackendAPIDeparture();
  const select = (next: FlowSelection) => {
    depart(() => setSelection(next));
  };
  const close = () => {
    depart(onClose);
  };
  return selection.valueRef?.kind === "column" ? (
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
