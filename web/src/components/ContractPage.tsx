// P7b (DESIGN §34.5, decisions.md mocker-p7-api-design D12): the «Контракт»
// tab — the workspace as ONE OpenAPI document, rendered read-only: paths →
// operations → «Запрос» / «Ответы» → the schema tree, every operation
// badged база / добавлено / изменено / удалено against the bound spec. The
// document is exactly what GET .../openapi.json answers (P7a), the badges
// are computed here from the two routes the editors already read
// (contractBadges.ts), and «Скачать» hands the SAME fetched document to the
// browser — no second fetch, no server-side marker.
//
// Read-only on purpose (the owner's call): the editors are the agent and the
// existing screens, so an operation that was added, changed or removed
// carries a link to the editor that owns it — «Кастомные» with the row
// opened, or «Endpoint'ы» with the operation selected — never an editor of
// its own. §14's word rule holds here as on every screen: «контракт»,
// «операция», «схема», «запрос», «ответ», «параметр».

import type { ReactElement } from "react";
import { useMemo, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Code,
  Group,
  Loader,
  Stack,
  Text,
  Title,
  UnstyledButton,
} from "@mantine/core";
import {
  IconAlertTriangle,
  IconChevronDown,
  IconChevronRight,
  IconDownload,
} from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useListEndpoints } from "@/api/generated/endpoints/endpoints.ts";
import { useListWorkspaceOperations } from "@/api/generated/operations/operations.ts";
import { useExportOpenAPI } from "@/api/generated/workspaces/workspaces.ts";
import { TabLink } from "./TabLink";
import { describeApiFailure } from "@/api/errors";
import {
  BADGE_COLOR,
  BADGE_LABEL,
  badgeFor,
  computeBadges,
  countBadges,
  docOperations,
  type BadgeKind,
  type DocOperation,
  type EditorLink,
  type OperationBadge,
} from "./contractBadges";
import { SchemaTree } from "./SchemaTree";

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

const METHOD_COLOR: Record<string, string> = {
  GET: "blue",
  POST: "green",
  PUT: "orange",
  PATCH: "yellow",
  DELETE: "red",
};

export function ContractPage({ id }: { id: number }): ReactElement {
  const contract = useExportOpenAPI(id);
  const operations = useListWorkspaceOperations(id);
  const endpoints = useListEndpoints(id);

  const pending = contract.isPending || operations.isPending || endpoints.isPending;
  const failed = contract.isError || operations.isError || endpoints.isError;
  const error = contract.error ?? operations.error ?? endpoints.error;

  return (
    <div data-testid="contract-page">
      <Stack gap="md">
        <Title order={1}>Контракт</Title>
        <Text size="sm" c="dimmed">
          Воркспейс как один документ OpenAPI: основа — привязанная спека, поверх неё — всё, что
          спроектировано здесь. Новая операция помечена «добавлено», изменённый ответ операции
          основы — «изменено», предложение убрать операцию — «удалено»; остальное — «база». Документ
          только для чтения: править операции — на вкладках{" "}
          <TabLink id={id} tab="endpoints" testId="contract-endpoints-link">
            «Кастомные»
          </TabLink>{" "}
          и{" "}
          <TabLink id={id} tab="operations" testId="contract-operations-link">
            «Endpoint&apos;ы»
          </TabLink>
          .
        </Text>
        {pending ? (
          <Group gap="xs">
            <Loader size="sm" />
            <Text size="sm" component="output">
              Загрузка…
            </Text>
          </Group>
        ) : failed ? (
          <Stack gap="sm" data-testid="contract-error">
            <Alert color="red" icon={<IconAlertTriangle size={18} />} role="alert">
              {describeApiFailure(error)}
            </Alert>
            <Button
              variant="default"
              w="fit-content"
              onClick={() => {
                void contract.refetch();
                void operations.refetch();
                void endpoints.refetch();
              }}
              data-testid="contract-retry"
            >
              Повторить
            </Button>
          </Stack>
        ) : contract.data.status !== 200 ||
          operations.data.status !== 200 ||
          endpoints.data.status !== 200 ? (
          <Alert
            color="red"
            icon={<IconAlertTriangle size={18} />}
            role="alert"
            data-testid="contract-error"
          >
            {describeApiFailure(null)}
          </Alert>
        ) : (
          <ContractView
            id={id}
            doc={contract.data.data}
            badges={computeBadges(operations.data.data, endpoints.data.data.endpoints)}
          />
        )}
      </Stack>
    </div>
  );
}

/** downloadDocument hands the already-fetched document to the browser as a
 * file — one createObjectURL, one click, the URL revoked afterwards. Never
 * a second fetch of the route: what the tab shows is what the file holds. */
export function downloadDocument(doc: unknown, fileName: string): void {
  const blob = new Blob([JSON.stringify(doc, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function ContractView({
  id,
  doc,
  badges,
}: {
  id: number;
  doc: unknown;
  badges: Map<string, OperationBadge>;
}): ReactElement {
  const ops = useMemo(() => docOperations(doc), [doc]);
  const counts = useMemo(() => countBadges(ops, badges), [ops, badges]);
  const info = isObject(doc) && isObject(doc["info"]) ? doc["info"] : {};
  const title = typeof info["title"] === "string" ? info["title"] : "";
  const version = typeof info["version"] === "string" ? info["version"] : "";

  const byPath = new Map<string, DocOperation[]>();
  for (const op of ops) {
    const list = byPath.get(op.path) ?? [];
    list.push(op);
    byPath.set(op.path, list);
  }

  return (
    <Stack gap="md">
      <Group gap="md" wrap="wrap" data-testid="contract-header">
        <Text size="sm">
          {title !== "" ? <strong>{title}</strong> : null}
          {version !== "" ? (
            <>
              {" "}
              <Code>{version}</Code>
            </>
          ) : null}
        </Text>
        {(["base", "added", "changed", "removed"] as BadgeKind[]).map((kind) => (
          <Badge
            key={kind}
            color={BADGE_COLOR[kind]}
            variant="light"
            data-testid={`contract-count-${kind}`}
          >
            {BADGE_LABEL[kind]}: {counts[kind]}
          </Badge>
        ))}
        <Button
          variant="default"
          size="xs"
          leftSection={<IconDownload size={14} />}
          onClick={() => downloadDocument(doc, `workspace-${id}-${version || "draft"}.json`)}
          data-testid="contract-download"
        >
          Скачать
        </Button>
      </Group>
      {ops.length === 0 ? (
        <Text size="sm" c="dimmed" data-testid="contract-empty">
          Пока ни одной операции: документ — пустой каркас OpenAPI 3.1. Добавьте эндпоинт на вкладке{" "}
          <TabLink id={id} tab="endpoints">
            «Кастомные»
          </TabLink>{" "}
          или{" "}
          <TabLink id={id} tab="overview">
            привяжите спеку
          </TabLink>
          .
        </Text>
      ) : (
        <Stack gap="sm" data-testid="contract-paths">
          {[...byPath.entries()].map(([path, list]) => (
            <Stack key={path} gap={4} data-testid="contract-path">
              <Code fw={600}>{path}</Code>
              {list.map((op) => (
                <OperationRow
                  key={`${op.method} ${op.path}`}
                  id={id}
                  op={op}
                  doc={doc}
                  badge={badgeFor(badges, op.method, op.path)}
                />
              ))}
            </Stack>
          ))}
        </Stack>
      )}
    </Stack>
  );
}

function OperationRow({
  id,
  op,
  doc,
  badge,
}: {
  id: number;
  op: DocOperation;
  doc: unknown;
  badge: OperationBadge;
}): ReactElement {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const o = op.operation;
  const summary = typeof o["summary"] === "string" ? o["summary"] : "";
  const operationId = typeof o["operationId"] === "string" ? o["operationId"] : "";
  const deprecated = o["deprecated"] === true;
  const websocket = o["x-websocket"] === true;

  function openEditor(link: EditorLink): void {
    if (link.screen === "endpoints") {
      void navigate({
        to: "/workspaces/$id/endpoints",
        params: { id },
        search: { endpointId: String(link.endpointId) },
      });
    } else {
      void navigate({
        to: "/workspaces/$id/operations",
        params: { id },
        search: { opKey: link.opKey },
      });
    }
  }

  return (
    <Stack gap={4} ml="md" data-testid="contract-op" data-badge={badge.kind}>
      <Group gap="xs" wrap="nowrap">
        <UnstyledButton
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          data-testid="contract-op-toggle"
          style={{ flex: 1 }}
        >
          <Group gap="xs" wrap="nowrap">
            {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
            <Badge size="sm" color={METHOD_COLOR[op.method] ?? "gray"} variant="filled">
              {op.method}
            </Badge>
            <Text size="sm" truncate>
              {summary !== "" ? summary : operationId !== "" ? operationId : op.path}
            </Text>
            {operationId !== "" && summary !== "" ? (
              <Text size="xs" c="dimmed">
                {operationId}
              </Text>
            ) : null}
            {deprecated ? (
              <Badge size="xs" color="red" variant="outline">
                устарело
              </Badge>
            ) : null}
            {websocket ? (
              <Badge size="xs" color="grape" variant="outline">
                WebSocket
              </Badge>
            ) : null}
            <Badge
              size="xs"
              color={BADGE_COLOR[badge.kind]}
              variant="light"
              data-testid={`contract-badge-${badge.kind}`}
            >
              {BADGE_LABEL[badge.kind]}
            </Badge>
          </Group>
        </UnstyledButton>
        {badge.link !== undefined ? (
          <Button
            size="compact-xs"
            variant="subtle"
            onClick={() => openEditor(badge.link as EditorLink)}
            data-testid="contract-op-link"
          >
            Открыть в редакторе
          </Button>
        ) : null}
      </Group>
      {open ? <OperationDetail op={op} doc={doc} /> : null}
    </Stack>
  );
}

function OperationDetail({ op, doc }: { op: DocOperation; doc: unknown }): ReactElement {
  const o = op.operation;
  const description = typeof o["description"] === "string" ? o["description"] : "";
  const tags = Array.isArray(o["tags"]) ? o["tags"].map(String) : [];
  const parameters = Array.isArray(o["parameters"]) ? o["parameters"].filter(isObject) : [];
  const requestBody = isObject(o["requestBody"]) ? o["requestBody"] : undefined;
  const requestContent =
    requestBody !== undefined && isObject(requestBody["content"]) ? requestBody["content"] : {};
  const responses = isObject(o["responses"]) ? o["responses"] : {};

  return (
    <Stack gap="xs" ml="lg" data-testid="contract-op-detail">
      {description !== "" ? (
        <Text size="sm" c="dimmed">
          {description}
        </Text>
      ) : null}
      {tags.length > 0 ? (
        <Group gap={4}>
          {tags.map((t) => (
            <Badge key={t} size="xs" variant="outline">
              {t}
            </Badge>
          ))}
        </Group>
      ) : null}

      <Text size="sm" fw={600}>
        Запрос
      </Text>
      {parameters.length === 0 && Object.keys(requestContent).length === 0 ? (
        <Text size="xs" c="dimmed" ml="md">
          без параметров и тела
        </Text>
      ) : null}
      {parameters.map((p, i) => (
        <Group key={i} gap={6} ml="md" wrap="nowrap" data-testid="contract-param">
          <Code>{String(p["name"] ?? "")}</Code>
          <Text size="xs" c="dimmed">
            параметр {String(p["in"] ?? "")}
            {p["required"] === true ? ", обязательный" : ""}
          </Text>
          {p["schema"] !== undefined ? (
            <SchemaTree schema={p["schema"]} doc={doc} depth={1} />
          ) : null}
        </Group>
      ))}
      {Object.entries(requestContent).map(([mediaType, mto]) => (
        <Stack key={mediaType} gap={2} ml="md">
          <Text size="xs" c="dimmed">
            тело {mediaType}
          </Text>
          <SchemaTree schema={isObject(mto) ? mto["schema"] : undefined} doc={doc} depth={1} />
        </Stack>
      ))}

      <Text size="sm" fw={600}>
        Ответы
      </Text>
      {Object.keys(responses)
        .sort()
        .map((status) => {
          const resp = responses[status];
          const r = isObject(resp) ? resp : {};
          const content = isObject(r["content"]) ? r["content"] : {};
          const respDescription = typeof r["description"] === "string" ? r["description"] : "";
          return (
            <Stack key={status} gap={2} ml="md" data-testid="contract-response">
              <Group gap={6}>
                <Code>{status}</Code>
                <Text size="xs" c="dimmed">
                  {respDescription}
                </Text>
              </Group>
              {Object.entries(content).map(([mediaType, mto]) => {
                const m = isObject(mto) ? mto : {};
                const examples = Array.isArray(m["examples"]) ? m["examples"] : [];
                return (
                  <Stack key={mediaType} gap={2} ml="md">
                    <Text size="xs" c="dimmed">
                      {mediaType === "text/event-stream" ? "поток событий" : mediaType}
                    </Text>
                    {m["schema"] !== undefined ? (
                      <SchemaTree schema={m["schema"]} doc={doc} depth={1} />
                    ) : null}
                    {examples.length > 0 ? (
                      <Stack gap={2} ml="md">
                        <Text size="xs" c="dimmed">
                          пример
                        </Text>
                        <Code block data-testid="contract-example">
                          {JSON.stringify(examples[0], null, 2)}
                        </Code>
                      </Stack>
                    ) : null}
                  </Stack>
                );
              })}
            </Stack>
          );
        })}
    </Stack>
  );
}
