import { useState } from "react";
import { Alert, Badge, Button, Code, Group, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type {
  BackendEffectiveGraphPins,
  BackendReadTarget,
  BackendSourceAssertionItem,
} from "@/api/generated/schemas";
import { backendReadQueryKey, readBackendAssertions } from "./backendGraphReads";
import { BackendReadError, backendReadTargetKey } from "./backendReadTargets";
import { LoadState, Pages } from "./BackendReadUI";
const wrap = { overflowWrap: "anywhere" as const, whiteSpace: "pre-wrap" as const };
type Props = {
  projectId: string;
  target: BackendReadTarget;
  recordType: "node" | "edge";
  id: string;
  supported: boolean;
  pins?: BackendEffectiveGraphPins;
  repositoryId?: string;
  providerNamespace?: string;
  assertionHash?: string;
  onEvidence: (id: string) => void;
};
export function BackendAssertions(props: Props) {
  if (!props.supported || props.target.proposal)
    return (
      <Text size="sm" c="dimmed">
        Отдельные утверждения доступны для source6 и предложений на его основе. Основания старых
        источников доступны отдельно.
      </Text>
    );
  return (
    <Assertions
      key={JSON.stringify([
        props.projectId,
        backendReadTargetKey(props.target),
        props.recordType,
        props.id,
        props.repositoryId,
        props.providerNamespace,
        props.assertionHash,
      ])}
      {...props}
    />
  );
}
function Assertions({
  projectId,
  target,
  recordType,
  id,
  pins,
  repositoryId,
  providerNamespace,
  assertionHash,
  onEvidence,
}: Props) {
  const [cursors, setCursors] = useState([""]);
  const input = {
    recordType,
    id,
    repositoryId,
    providerNamespace,
    limit: 100,
    cursor: cursors.at(-1) ?? "",
  };
  const query = useQuery({
    queryKey: backendReadQueryKey("assertions", projectId, target, { ...input, assertionHash }),
    queryFn: async ({ signal }) => {
      const page = await readBackendAssertions(projectId, target, input, signal, pins);
      if (
        assertionHash &&
        (page.items.length !== 1 || page.items[0]?.assertion.assertionHash !== assertionHash)
      )
        throw new BackendReadError(
          "Полученное утверждение не соответствует выбранной идентичности.",
        );
      return page;
    },
    retry: false,
    staleTime: Infinity,
  });
  const page = query.data;
  return (
    <Stack gap="sm" aria-label="Утверждения источников">
      <Title order={4}>Утверждения источников</Title>
      {target.changeProposal && (
        <Alert color="blue">
          Утверждения базового источника. Изменения предложения показаны отдельно как намерение.
        </Alert>
      )}
      <LoadState query={query} label="утверждений" />
      {page?.items.length === 0 && <Text c="dimmed">Утверждений нет</Text>}
      {page?.items.map((item) => (
        <Claim
          key={JSON.stringify([
            item.assertion.owner.repositoryId,
            item.assertion.owner.providerNamespace,
            item.assertion.assertionHash,
          ])}
          item={item}
          expanded={item.assertion.assertionHash === assertionHash}
          onEvidence={onEvidence}
        />
      ))}
      <Pages
        label="утверждения"
        cursors={cursors}
        next={page?.nextCursor}
        busy={query.isFetching}
        setCursors={setCursors}
      />
    </Stack>
  );
}
function Claim({
  item,
  expanded,
  onEvidence,
}: {
  item: BackendSourceAssertionItem;
  expanded: boolean;
  onEvidence: (id: string) => void;
}) {
  const { assertion, currentness, selections, conflicts } = item;
  const selected = selections.filter(
    (s) =>
      s.select.repositoryId === assertion.owner.repositoryId &&
      s.select.providerNamespace === assertion.owner.providerNamespace &&
      s.select.assertionHash === assertion.assertionHash,
  );
  return (
    <details open={expanded || undefined}>
      <summary style={{ cursor: "pointer", ...wrap }}>
        {assertion.owner.providerNamespace} · {assertion.externalKey}
      </summary>
      <Stack gap="xs" mt="xs">
        <Text size="sm" style={wrap}>
          Репозиторий: {assertion.owner.repositoryId}
        </Text>
        <Text size="xs" style={wrap}>
          Хеш утверждения: {assertion.assertionHash}
        </Text>
        <Group>
          <Badge color={currentness.own.status === "current" ? "teal" : "yellow"}>
            Источник: {currentness.own.status}
          </Badge>
          <Badge color={currentness.dependency.status === "current" ? "teal" : "yellow"}>
            Зависимости: {currentness.dependency.status}
          </Badge>
        </Group>
        <Text size="sm" style={wrap}>
          {[...currentness.own.reasons, ...currentness.dependency.reasons].join(", ")}
        </Text>
        <Code block style={wrap}>
          {JSON.stringify(assertion.payload, null, 2)}
        </Code>
        {selected.length > 0 ? (
          <Text size="sm">
            Выбранная поддержка: {selected.map((s) => JSON.stringify(s.property)).join(", ")}
          </Text>
        ) : (
          <Text size="sm" c="dimmed">
            Явного выбора этого утверждения нет.
          </Text>
        )}
        {currentness.fields.length > 0 && (
          <details>
            <summary>Актуальность полей</summary>
            <Code block style={wrap}>
              {JSON.stringify(currentness.fields, null, 2)}
            </Code>
          </details>
        )}
        {conflicts.length > 0 && (
          <details>
            <summary>Расхождения ({conflicts.length})</summary>
            <Code block style={wrap}>
              {JSON.stringify(conflicts, null, 2)}
            </Code>
          </details>
        )}
        {selections.length > 0 && (
          <details>
            <summary>Решения выбора</summary>
            <Code block style={wrap}>
              {JSON.stringify(selections, null, 2)}
            </Code>
          </details>
        )}
        {assertion.evidenceIds.map((evidenceId) => (
          <Button
            key={evidenceId}
            variant="subtle"
            h="auto"
            styles={{ label: wrap }}
            onClick={() => onEvidence(evidenceId)}
          >
            Открыть основание {evidenceId}
          </Button>
        ))}
      </Stack>
    </details>
  );
}
