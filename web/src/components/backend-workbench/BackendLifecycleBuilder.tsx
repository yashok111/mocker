import { useEffect, useRef, useState } from "react";
import { Alert, Button, Group, Stack, TextInput } from "@mantine/core";
import { buildBackendLifecycle } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendDiagramTarget,
  BackendLifecycleDocument,
  BuildBackendLifecycleRequest,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";
export function BackendLifecycleBuilder({
  projectId,
  target,
  disabled,
  onCandidate,
}: {
  projectId: string;
  target?: BackendDiagramTarget;
  disabled: boolean;
  onCandidate: (d: BackendLifecycleDocument) => void;
}) {
  const [entity, setEntity] = useState("");
  const [fields, setFields] = useState("");
  const [selection, setSelection] = useState("");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const scope = JSON.stringify([projectId, target]);
  useEffect(
    () => () => {
      abort.current?.abort();
    },
    [scope],
  );
  const input = (): BuildBackendLifecycleRequest => {
    if (!target) throw new Error("Выберите точный target");
    return {
      target,
      entity: JSON.parse(entity),
      stateFields: JSON.parse(fields),
      stateDiagram: JSON.parse(selection),
      ...(reason ? { compoundMappingReason: reason } : {}),
    };
  };
  async function build() {
    if (disabled || busy) return;
    const controller = new AbortController();
    abort.current = controller;
    setBusy(true);
    setError("");
    try {
      const r = await buildBackendLifecycle(projectId, input(), { signal: controller.signal });
      controller.signal.throwIfAborted();
      if (r.status !== 200) throw new Error("Кандидат lifecycle недоступен");
      onCandidate(r.data.document);
    } catch (e) {
      if (!controller.signal.aborted) setError(describeApiFailureDetailed(e));
    } finally {
      if (abort.current === controller) setBusy(false);
    }
  }
  return (
    <Stack>
      <Group grow>
        <TextInput
          label="Сущность lifecycle (Ref JSON)"
          value={entity}
          onChange={(e) => setEntity(e.currentTarget.value)}
          disabled={disabled || busy}
        />
        <TextInput
          label="Поля состояния (Ref JSON массив)"
          value={fields}
          onChange={(e) => setFields(e.currentTarget.value)}
          disabled={disabled || busy}
        />
      </Group>
      <TextInput
        label="Точный States artifact (locator и rowId JSON)"
        value={selection}
        onChange={(e) => setSelection(e.currentTarget.value)}
        disabled={disabled || busy}
      />
      <TextInput
        label="Причина составного mapping"
        value={reason}
        onChange={(e) => setReason(e.currentTarget.value)}
        disabled={disabled || busy}
      />
      <Group>
        <Button
          variant="default"
          disabled={disabled || busy || !target || !entity || !fields || !selection}
          loading={busy}
          onClick={() => void build()}
        >
          Предложить lifecycle из States
        </Button>
        <Button
          variant="default"
          disabled={disabled || busy || !target || !entity || !fields}
          onClick={() => {
            try {
              if (!target) return;
              onCandidate({
                format: "backend-diagram-v1",
                kind: "lifecycle",
                target,
                payload: {
                  entity: JSON.parse(entity),
                  stateFields: JSON.parse(fields),
                  ...(reason ? { compoundMappingReason: reason } : {}),
                  states: [],
                  transitions: [],
                  rules: [],
                  coverage: "partial",
                  coverageOrigin: { kind: "authored", reason: "Explicit lifecycle authoring" },
                },
              });
              setError("");
            } catch {
              setError("Проверьте JSON сущности и полей состояния");
            }
          }}
        >
          Создать lifecycle
        </Button>
      </Group>
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
    </Stack>
  );
}
