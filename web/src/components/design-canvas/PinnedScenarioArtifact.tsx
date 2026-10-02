import { Button, Code, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { readPinnedScenario } from "../backend-workbench/backendArtifactReads";
import { databaseWrap, useDatabaseCancellation } from "../backend-workbench/backendDatabaseReads";
import { LoadState } from "../backend-workbench/BackendGraphInventory";
import { pinnedBackendReturnHref, type PinnedAPIContext } from "../api-designer/PinnedAPIArtifact";
export function PinnedScenarioArtifact({
  artifactId,
  pin,
  dirty,
  currentRevisionId,
  onClose,
}: {
  artifactId: string;
  pin: PinnedAPIContext;
  dirty?: boolean;
  currentRevisionId?: string;
  onClose?: () => void;
}) {
  const key = ["pinned-scenario-artifact", artifactId, JSON.stringify(pin)];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) =>
      readPinnedScenario(artifactId, pin.pinnedRevisionId!, pin.pinnedHash!, signal),
  });
  const s = query.data;
  const href = pinnedBackendReturnHref(pin);
  return (
    <Paper
      component="section"
      aria-label="Неизменяемый снимок сценария"
      withBorder
      p="md"
      style={{ minWidth: 0, maxWidth: "100%" }}
    >
      <Stack>
        <Title order={3}>Закреплённый сценарий</Title>
        <Text style={databaseWrap}>
          Сценарий {artifactId} · ревизия {pin.pinnedRevisionId} · сохранённый хеш {pin.pinnedHash}
        </Text>
        {currentRevisionId && (
          <Text style={databaseWrap}>
            Текущий черновик {currentRevisionId} ·{" "}
            {dirty ? "есть несохранённые изменения" : "изменений в буфере нет"}
          </Text>
        )}
        <LoadState query={query} label="снимка сценария" />
        {s && (
          <>
            <Text style={databaseWrap}>
              Версия {s.version} · {s.typedStatus} · envelope {s.envelopeVerification} ·{" "}
              {s.hashPolicy}
            </Text>
            <Text style={databaseWrap}>Хеш сырого документа: {s.documentHash}</Text>
            {s.envelopeVerification === "verified" ? (
              <Text style={databaseWrap}>Проверенный хеш конверта: {s.contentHash}</Text>
            ) : (
              <Text c="orange">
                Сохранённый хеш конверта не проверен. Неподдерживаемый снимок доступен для чтения.
              </Text>
            )}
            <Text fw={600}>Сырой документ</Text>
            <Code
              block
              component="pre"
              tabIndex={0}
              style={{ ...databaseWrap, maxHeight: 400, overflow: "auto" }}
            >
              {s.documentJSON}
            </Code>
            <Text fw={600}>Сохранённые формы</Text>
            <Code
              block
              component="pre"
              tabIndex={0}
              style={{ ...databaseWrap, maxHeight: 240, overflow: "auto" }}
            >
              {s.formDraftsJSON}
            </Code>
          </>
        )}
        {href && (
          <Button component="a" href={href} variant="default" style={{ whiteSpace: "normal" }}>
            Вернуться к закреплённому источнику
          </Button>
        )}
        {onClose && (
          <Button onClick={onClose} variant="subtle">
            Закрыть снимок сценария
          </Button>
        )}
      </Stack>
    </Paper>
  );
}
