import { Alert, Button, Code, Group, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { queryBackendNamespacedArtifact } from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendDiagramRef,
  BackendDiagramTarget,
  QueryBackendNamespacedArtifactRequest,
} from "@/api/generated/schemas";
import { describeApiFailureDetailed } from "@/api/errors";

type NamespacedRef = Extract<BackendDiagramRef, { kind: "namespaced_artifact" }>;
export function BackendNamespacedArtifactInspector({
  projectId,
  target,
  targetHash,
  reference,
  onClose,
}: {
  projectId: string;
  target: BackendDiagramTarget;
  targetHash: string;
  reference: NamespacedRef;
  onClose: () => void;
}) {
  const { namespace, locator } = reference.namespacedLocator;
  const query = useQuery({
    queryKey: [
      "backend-namespaced-artifact",
      projectId,
      JSON.stringify([target, targetHash, reference]),
    ],
    retry: false,
    queryFn: async ({ signal }) => {
      const pin = locator.pin;
      if ((pin.kind !== "api_design" && pin.kind !== "design_scenario") || !("contentHash" in pin))
        throw new Error("Некорректный namespaced owner pin");
      const input: QueryBackendNamespacedArtifactRequest = {
        target,
        targetHash,
        namespace,
        artifact: { kind: pin.kind, id: pin.id },
        view: locator.view,
        limit: 100,
        ...(locator.embedded ? { embeddedContractId: locator.embedded.contractId } : {}),
      };
      const response = await queryBackendNamespacedArtifact(projectId, input, { signal });
      signal.throwIfAborted();
      if (
        response.status !== 200 ||
        response.data.targetHash !== targetHash ||
        response.data.pin.namespace.scope !== namespace.scope ||
        response.data.pin.namespace.installationId !== namespace.installationId ||
        response.data.pin.pin.id !== locator.pin.id ||
        response.data.pin.pin.revisionId !== locator.pin.revisionId ||
        !("contentHash" in response.data.pin.pin) ||
        response.data.pin.pin.contentHash !== pin.contentHash ||
        response.data.pin.pin.kind !== pin.kind
      )
        throw new Error("Ответ не совпадает с точным namespaced pin");
      return response.data;
    },
  });
  return (
    <Stack aria-label="Точный namespaced artifact">
      <Text>
        {namespace.scope} · {namespace.installationId} · {locator.pin.kind} {locator.pin.id} ·
        revision {locator.pin.revisionId}
      </Text>
      {query.isPending && <Text role="status">Чтение точной ссылки…</Text>}
      {query.error && <Alert color="red">{describeApiFailureDetailed(query.error)}</Alert>}
      {query.data?.status === "foreign_unresolved" && (
        <Alert color="yellow">
          Foreign ref не разрешён. Локальный owner по совпадающему numeric ID не читался. Явный
          mapping задаётся при Preview импорта.
        </Alert>
      )}
      {query.data && (
        <Code block>
          {JSON.stringify(
            query.data.projection ?? {
              apiBindings: query.data.apiBindings,
              editorBindings: query.data.editorBindings,
            },
            null,
            2,
          )}
        </Code>
      )}
      <Group>
        <Button variant="default" onClick={onClose}>
          Закрыть namespaced artifact
        </Button>
      </Group>
    </Stack>
  );
}
