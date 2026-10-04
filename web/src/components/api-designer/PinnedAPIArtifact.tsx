import { Alert, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import {
  getAPIArtifactSnapshot,
  getApiDesignRevision,
} from "@/api/generated/api-designs/api-designs";
import {
  exactArtifactId,
  safeLegacyId,
  strictAuthoredPointer,
} from "../backend-workbench/backendAPIArtifactReads";
import {
  databaseButtonStyles,
  databaseWrap,
  useDatabaseCancellation,
} from "../backend-workbench/backendDatabaseReads";
import { LoadState } from "../backend-workbench/BackendGraphInventory";
import { listCanvasOperations } from "../design-canvas/canvasOperations";
import { escapeJsonPointerToken, isRecord } from "./documentModel";
import { findJsonPointerRange } from "./monaco/jsonPointerRange";
import type { BackendReadTarget } from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backend-workbench/backendReadTargets";

const pinButtonProps = { h: "auto", py: "xs", maw: "100%", styles: databaseButtonStyles };

export type PinnedAPIContext = {
  projectionView?: string;
  embeddedContractId?: string;
  pinnedRevisionId?: string;
  pinnedHash?: string;
  pinnedObjectKey?: string;
  pinnedPointer?: string;
  pinnedSelectorPointer?: string;
  returnProjectId?: string;
  returnRevisionId?: string;
  returnChangeProposalId?: string;
  returnProposalRevisionId?: string;
  returnSourceNodeId?: string;
};
const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
export function pinnedBackendReturnTarget(pin: PinnedAPIContext): BackendReadTarget | undefined {
  if (
    !pin.returnProjectId ||
    !uuid.test(pin.returnProjectId) ||
    (pin.returnSourceNodeId !== undefined && !uuid.test(pin.returnSourceNodeId))
  )
    return undefined;
  const full =
    pin.returnChangeProposalId !== undefined || pin.returnProposalRevisionId !== undefined;
  if (full && pin.returnRevisionId !== undefined) return undefined;
  const target: BackendReadTarget = full
    ? {
        changeProposal: {
          proposalId: pin.returnChangeProposalId ?? "",
          proposalRevisionId: pin.returnProposalRevisionId ?? "",
        },
      }
    : { revisionId: pin.returnRevisionId ?? "" };
  try {
    backendReadTargetKey(target);
  } catch {
    return undefined;
  }
  return target;
}
export function pinnedBackendReturnHref(pin: PinnedAPIContext): string | undefined {
  const target = pinnedBackendReturnTarget(pin);
  if (!target) return undefined;
  const search = new URLSearchParams(
    target.changeProposal
      ? {
          changeProposalId: target.changeProposal.proposalId,
          proposalRevisionId: target.changeProposal.proposalRevisionId,
        }
      : { revisionId: target.revisionId! },
  );
  if (pin.returnSourceNodeId) {
    search.set("recordId", pin.returnSourceNodeId);
    search.set("recordType", "node");
  }
  return `/backend-projects/${pin.returnProjectId}?${search}`;
}
function rawPointer(root: unknown, pointer: string): unknown {
  if (!strictAuthoredPointer(pointer)) throw new Error("Некорректный авторский путь снимка API");
  let current = root;
  for (const escaped of pointer.slice(1).split("/")) {
    const token = escaped.replaceAll("~1", "/").replaceAll("~0", "~");
    if (Array.isArray(current)) {
      if (
        !/^(0|[1-9][0-9]*)$/.test(token) ||
        !Number.isSafeInteger(Number(token)) ||
        Number(token) >= current.length
      )
        return undefined;
      current = current[Number(token)];
    } else if (isRecord(current) && Object.hasOwn(current, token)) current = current[token];
    else return undefined;
  }
  return current;
}
export function resolveRawConsumerPointer(root: unknown, path: string, method: string): string {
  let pointer = `/paths/${escapeJsonPointerToken(path)}`;
  const visited = new Set<string>();
  for (let depth = 0; depth < 128; depth++) {
    if (visited.has(pointer)) throw new Error("В цепочке Path Item обнаружен цикл");
    visited.add(pointer);
    const item = rawPointer(root, pointer);
    if (!isRecord(item)) throw new Error("Path Item отсутствует в точном снимке API");
    if (Object.hasOwn(item, method)) return `${pointer}/${escapeJsonPointerToken(method)}`;
    if (typeof item.$ref !== "string") throw new Error("Операция отсутствует в точном снимке API");
    if (!item.$ref.startsWith("#/")) throw new Error("Нельзя следовать внешней ссылке Path Item");
    try {
      pointer = decodeURIComponent(item.$ref.slice(1));
    } catch {
      throw new Error("Некорректная локальная ссылка Path Item");
    }
    if (!strictAuthoredPointer(pointer)) throw new Error("Некорректный путь Path Item");
  }
  throw new Error("Превышена глубина цепочки Path Item 128");
}
export async function readPinnedAPIArtifact(
  artifactId: string,
  pin: PinnedAPIContext,
  signal: AbortSignal,
) {
  if (
    !exactArtifactId(artifactId) ||
    !exactArtifactId(pin.pinnedRevisionId) ||
    !pin.pinnedHash ||
    !/^[a-f0-9]{64}$/.test(pin.pinnedHash)
  )
    throw new Error("Неверная точная ссылка снимка API");
  if (pin.pinnedObjectKey !== undefined && pin.pinnedSelectorPointer !== undefined)
    throw new Error("Снимок API требует один селектор");
  signal.throwIfAborted();
  const response = await getAPIArtifactSnapshot(artifactId, pin.pinnedRevisionId, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось прочитать снимок API");
  const snapshot = response.data;
  if (
    snapshot.artifactId !== artifactId ||
    snapshot.revisionId !== pin.pinnedRevisionId ||
    snapshot.contentHash !== pin.pinnedHash ||
    bytesToHex(sha256(new TextEncoder().encode(snapshot.document))) !== pin.pinnedHash
  )
    throw new Error("Не совпадает идентичность или хеш сырого снимка API");
  const root: unknown = JSON.parse(snapshot.document);
  let pointer = pin.pinnedSelectorPointer ?? pin.pinnedPointer;
  let consumer: string | undefined;
  let limitation: string | undefined;
  if (pin.pinnedObjectKey !== undefined) {
    const designId = safeLegacyId(artifactId),
      revisionId = safeLegacyId(pin.pinnedRevisionId);
    if (designId === undefined || revisionId === undefined)
      limitation =
        "Числовой API владельца не поддерживает точную идентичность потребителя для этого ID. Сырой снимок сохранён без округления.";
    else {
      const owner = await getApiDesignRevision(designId, revisionId, { signal });
      signal.throwIfAborted();
      if (
        owner.status !== 200 ||
        safeLegacyId(owner.data.id) !== revisionId ||
        safeLegacyId(owner.data.designId) !== designId ||
        owner.data.hash !== snapshot.contentHash
      )
        throw new Error("Не совпадает точная идентичность владельца снимка API");
      const hydrated: unknown = JSON.parse(owner.data.document);
      if (!isRecord(hydrated)) throw new Error("Неверная проекция идентичности снимка API");
      const candidates = listCanvasOperations(hydrated).filter(
        (operation) => operation.key === pin.pinnedObjectKey,
      );
      if (candidates.length > 1)
        throw new Error("Потребитель с точным object key неоднозначен в снимке API");
      if (candidates.length === 0)
        limitation =
          "Потребитель с точным object key отсутствует в целевом снимке API. Сырой документ доступен для явного переназначения.";
      else {
        const operation = candidates[0]!;
        const authored = resolveRawConsumerPointer(
          root,
          operation.location.path,
          operation.location.method,
        );
        if (pointer !== undefined && pointer !== authored)
          throw new Error("Авторский путь не соответствует закреплённому потребителю снимка API");
        pointer = authored;
        consumer = `${operation.location.method.toUpperCase()} ${operation.location.path} · object key ${pin.pinnedObjectKey}${operation.inherited ? " · наследуется из Path Item" : ""}`;
      }
    }
  }
  let selection: unknown;
  if (pointer !== undefined) {
    selection = rawPointer(root, pointer);
    if (selection === undefined)
      limitation = `Выбранный авторский объект ${pointer} отсутствует в неизменяемом снимке.`;
  }
  signal.throwIfAborted();
  return { snapshot, pointer, selection, consumer, limitation };
}
export function PinnedAPIArtifact({
  artifactId,
  pin,
  currentRevisionId,
  dirty,
  onOpenCurrent,
}: {
  artifactId: string;
  pin: PinnedAPIContext;
  currentRevisionId: string;
  dirty: boolean;
  onOpenCurrent: () => void;
}) {
  const key = ["pinned-api-artifact", artifactId, JSON.stringify(pin)];
  useDatabaseCancellation(key);
  const query = useQuery({
    queryKey: key,
    retry: false,
    staleTime: Infinity,
    queryFn: ({ signal }) => readPinnedAPIArtifact(artifactId, pin, signal),
  });
  const result = query.data;
  const source = result?.snapshot.document;
  const range =
    source !== undefined && result?.pointer && result.selection !== undefined
      ? findJsonPointerRange(source, result.pointer)
      : null;
  const href = pinnedBackendReturnHref(pin);
  return (
    <Paper
      component="section"
      withBorder
      p="md"
      aria-label="Неизменяемый снимок API"
      data-testid="pinned-api-artifact"
      style={{ minWidth: 0, maxWidth: "100%" }}
    >
      <Stack>
        <Title order={2}>Закреплённый снимок API{result ? ` · ${result.snapshot.name}` : ""}</Title>
        <Text size="sm" style={databaseWrap}>
          API {artifactId} · ревизия {pin.pinnedRevisionId} · хеш {pin.pinnedHash}
        </Text>
        <Text size="sm" style={databaseWrap}>
          Текущий черновик: {currentRevisionId} ·{" "}
          {dirty ? "есть несохранённые изменения" : "изменений в буфере нет"}
        </Text>
        {pin.pinnedRevisionId !== currentRevisionId && (
          <Alert color="yellow">
            Редактор содержит текущий черновик другой ревизии. Эта панель показывает неизменяемый
            сырой снимок отдельно.
          </Alert>
        )}
        <Group>
          <Button {...pinButtonProps} data-testid="pinned-api-open-current" onClick={onOpenCurrent}>
            Открыть текущий черновик
          </Button>
          {href && (
            <Button {...pinButtonProps} component="a" variant="default" href={href}>
              Вернуться к источнику бэкенда
            </Button>
          )}
        </Group>
        <LoadState query={query} label="закреплённого снимка API" />
        {result?.consumer && <Text style={databaseWrap}>Потребитель: {result.consumer}</Text>}
        {result?.pointer && (
          <Text style={databaseWrap}>Выбранный авторский путь: {result.pointer}</Text>
        )}
        {result?.limitation && <Alert color="yellow">{result.limitation}</Alert>}
        {source !== undefined && (
          <Code
            component="pre"
            block
            data-testid="pinned-api-raw"
            style={{ ...databaseWrap, maxHeight: 480, overflow: "auto" }}
            tabIndex={0}
          >
            {range ? (
              <>
                {source.slice(0, range.startOffset)}
                <mark data-testid="pinned-api-selection">
                  {source.slice(range.startOffset, range.endOffset)}
                </mark>
                {source.slice(range.endOffset)}
              </>
            ) : (
              source
            )}
          </Code>
        )}
      </Stack>
    </Paper>
  );
}
