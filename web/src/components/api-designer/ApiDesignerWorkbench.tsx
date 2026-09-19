import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { ReactElement, ReactNode } from "react";
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Code,
  Drawer,
  Group,
  Loader,
  NativeSelect,
  SegmentedControl,
  Stack,
  Switch,
  Tabs,
  Text,
  TextInput,
  Title,
  Tooltip,
  UnstyledButton,
} from "@mantine/core";
import { useDisclosure, useMediaQuery } from "@mantine/hooks";
import {
  IconAlertTriangle,
  IconBraces,
  IconCheck,
  IconChevronLeft,
  IconCopy,
  IconDeviceFloppy,
  IconGitCompare,
  IconHistory,
  IconListDetails,
  IconMaximize,
  IconMenu2,
  IconMinimize,
  IconPlus,
  IconRefresh,
  IconRestore,
  IconSearch,
  IconSend,
  IconTestPipe,
  IconTrash,
} from "@tabler/icons-react";
import { useBlocker, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGetApiDesignQueryKey,
  useCloseApiDesignChangeSet,
  useCreateApiDesignChangeSet,
  useGetApiDesign,
  useGetApiDesignDiff,
  useGetApiDesignRevision,
  usePublishApiDesignReview,
  useRequestApiDesignReview,
  useRestoreApiDesignRevision,
  useSaveApiDesignDraft,
  useValidateApiDesign,
} from "@/api/generated/api-designs/api-designs.ts";
import type {
  ApiDesignChange,
  ApiDesignChangeSet,
  ApiDesignDetail,
  ApiDesignDiagnostic,
  ApiDesignReview,
  ApiDesignRevisionSummary,
} from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";
import { formatTimestamp } from "@/format";
import { createFormDraftStore, DocumentForm } from "./DocumentForm";
import type { ApiDocument, DocumentSelection } from "./documentModel";
import {
  copyOperation,
  createOperation,
  createSchema,
  deleteOperation,
  deleteSchema,
  getOperation,
  getSchema,
  isRecord,
  listOperations,
  listSchemas,
  operationPointer,
  schemaPointer,
  updateOperation,
} from "./documentModel";
import { MockTester } from "./MockTester";
import { SourceDiff, SourceEditor } from "./SourceEditor";
import { hasUnsafeJsonNumber } from "./jsonNumberPrecision";
import classes from "./ApiDesigner.module.css";

const POLL_MS = 5_000;
type CanvasView = "documentation" | "editor" | "compare" | "review";
type EditorMode = "form" | "source";
type InspectorView = "changes" | "history" | "checks" | "mock";

type StoredDraft = {
  text: string;
  baseDocument: string;
  baseVersion: number;
  baseRevisionId: number;
  formDrafts: string;
  savedAt: number;
};

export function ApiDesignerWorkbench({
  id,
  reviewId,
}: {
  id: number;
  reviewId?: number;
}): ReactElement {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const narrow = useMediaQuery("(max-width: 62em)", false, { getInitialValueInEffect: false });
  const [treeOpened, tree] = useDisclosure(false);
  const [inspectorOpened, inspector] = useDisclosure(false);
  const [focusMode, setFocusMode] = useState(false);
  const treeComposer = useApiTreeComposer();
  const [view, setView] = useState<CanvasView>(reviewId === undefined ? "documentation" : "review");
  const [editorMode, setEditorMode] = useState<EditorMode>("form");
  const [inspectorView, setInspectorView] = useState<InspectorView>("changes");
  const [selection, setSelection] = useState<DocumentSelection>({ kind: "document" });
  const [search, setSearch] = useState("");
  const [changedOnly, setChangedOnly] = useState(false);
  const [buffer, setBuffer] = useState("");
  const [baseDocument, setBaseDocument] = useState("");
  const [baseVersion, setBaseVersion] = useState(0);
  const [baseRevisionId, setBaseRevisionId] = useState(0);
  const [externalDetail, setExternalDetail] = useState<ApiDesignDetail | null>(null);
  const [summary, setSummary] = useState("");
  const [conflict, setConflict] = useState(false);
  const [diagnostics, setDiagnostics] = useState<ApiDesignDiagnostic[]>([]);
  const [validatedDocument, setValidatedDocument] = useState<string | null>(null);
  const [activeChangeSetId, setActiveChangeSetId] = useState<number | null>(null);
  const [changeSetTitle, setChangeSetTitle] = useState("");
  const [reviewSummary, setReviewSummary] = useState("");
  const [selectedReviewId, setSelectedReviewId] = useState<number>();
  const [optimisticReview, setOptimisticReview] = useState<ApiDesignReview | null>(null);
  const [selectedRevisionId, setSelectedRevisionId] = useState<number | null>(null);
  const [comparisonToRevisionId, setComparisonToRevisionId] = useState<number | null>(null);
  const [focusPointer, setFocusPointer] = useState<string>();
  const initialized = useRef(false);
  const bufferRef = useRef(buffer);
  const submittedFormDrafts = useRef("{}");
  const [draftStore] = useState(createFormDraftStore);
  const formDraft = useSyncExternalStore(
    draftStore.subscribe,
    draftStore.getSnapshot,
    draftStore.getSnapshot,
  );

  useEffect(() => {
    bufferRef.current = buffer;
  }, [buffer]);

  const detailQuery = useGetApiDesign(id, {
    query: {
      refetchInterval: POLL_MS,
      refetchOnWindowFocus: true,
      retry: false,
    },
  });
  const detail = detailQuery.data?.status === 200 ? detailQuery.data.data : null;
  const documentDirty = baseDocument !== "" && buffer !== baseDocument;
  const dirty = documentDirty || formDraft.dirty;

  useBlocker({
    shouldBlockFn: () =>
      dirty &&
      !window.confirm(
        "Есть несохранённые изменения. Покинуть редактор и сохранить буфер в браузере?",
      ),
    enableBeforeUnload: dirty,
    withResolver: false,
  });

  const adoptServer = useCallback(
    (next: ApiDesignDetail): void => {
      setBuffer(next.draft.document);
      setBaseDocument(next.draft.document);
      setBaseVersion(next.design.version);
      setBaseRevisionId(next.draft.id);
      setExternalDetail(null);
      setConflict(false);
      setDiagnostics([]);
      setValidatedDocument(null);
      draftStore.clear();
      localStorage.removeItem(storageKey(id));
    },
    [draftStore, id],
  );

  useEffect(() => {
    if (detail === null) return;
    if (!initialized.current) {
      initialized.current = true;
      const stored = readStoredDraft(id);
      if (stored !== null) {
        draftStore.hydrate(stored.formDrafts);
        // oxlint-disable-next-line react/set-state-in-effect -- Query data and a persisted draft initialize the editable buffer together.
        setBuffer(stored.text);
        setBaseDocument(stored.baseDocument);
        setBaseVersion(stored.baseVersion);
        setBaseRevisionId(stored.baseRevisionId);
        if (detail.design.version !== stored.baseVersion) setExternalDetail(detail);
      } else {
        setBuffer(detail.draft.document);
        setBaseDocument(detail.draft.document);
        setBaseVersion(detail.design.version);
        setBaseRevisionId(detail.draft.id);
      }
      setActiveChangeSetId(detail.changeSets.find((set) => set.status === "open")?.id ?? null);
      return;
    }
    if (detail.design.version === baseVersion) return;
    if (dirty) {
      setExternalDetail(detail);
    } else {
      adoptServer(detail);
    }
  }, [adoptServer, baseVersion, detail, dirty, draftStore, id]);

  useEffect(() => {
    if (!initialized.current) return;
    if (dirty) {
      const stored: StoredDraft = {
        text: buffer,
        baseDocument,
        baseVersion,
        baseRevisionId,
        formDrafts: draftStore.serialize(),
        savedAt: Date.now(),
      };
      localStorage.setItem(storageKey(id), JSON.stringify(stored));
    } else {
      localStorage.removeItem(storageKey(id));
    }
  }, [baseDocument, baseRevisionId, baseVersion, buffer, dirty, draftStore, formDraft, id]);

  function cacheDetail(
    next: ApiDesignDetail,
    submittedDocument?: string,
    submittedDrafts?: string,
  ): void {
    if (
      submittedDocument !== undefined &&
      (bufferRef.current !== submittedDocument ||
        (submittedDrafts !== undefined && draftStore.serialize() !== submittedDrafts))
    ) {
      setBaseDocument(next.draft.document);
      setBaseVersion(next.design.version);
      setBaseRevisionId(next.draft.id);
      setExternalDetail(null);
      setConflict(false);
      setDiagnostics([]);
      setValidatedDocument(null);
    } else {
      adoptServer(next);
    }
    queryClient.setQueryData(getGetApiDesignQueryKey(id), {
      status: 200,
      data: next,
      headers: new Headers(),
    });
  }

  const save = useSaveApiDesignDraft({
    mutation: {
      onSuccess: (response, variables) => {
        if (response.status === 200) {
          cacheDetail(response.data, variables.data.document, submittedFormDrafts.current);
          setSummary("");
        }
      },
      onError: (error) => {
        if (error instanceof ApiFailure && error.code === "design_conflict") setConflict(true);
        if (error instanceof ApiFailure && error.code === "design_invalid") {
          setDiagnostics(readDiagnostics(error.details));
        }
      },
    },
  });
  const validate = useValidateApiDesign({
    mutation: {
      onSuccess: (response, variables) => {
        if (response.status === 200) {
          setDiagnostics(response.data.diagnostics);
          setValidatedDocument(variables.data.document);
        }
      },
      onError: (error, variables) => {
        if (error instanceof ApiFailure && error.code === "design_invalid") {
          setDiagnostics(readDiagnostics(error.details));
          setValidatedDocument(variables.data.document);
        }
      },
    },
  });
  const createChangeSet = useCreateApiDesignChangeSet({
    mutation: {
      onSuccess: (response) => {
        if (response.status !== 201) return;
        setActiveChangeSetId(response.data.id);
        setChangeSetTitle("");
        void detailQuery.refetch();
      },
    },
  });
  const closeChangeSet = useCloseApiDesignChangeSet({
    mutation: {
      onSuccess: () => {
        setActiveChangeSetId(null);
        void detailQuery.refetch();
      },
    },
  });
  const requestReview = useRequestApiDesignReview({
    mutation: {
      onSuccess: (response) => {
        if (response.status !== 201) return;
        setSelectedReviewId(response.data.id);
        setOptimisticReview(response.data);
        setSelectedRevisionId(null);
        setComparisonToRevisionId(null);
        setReviewSummary("");
        setView("review");
        void detailQuery.refetch();
      },
    },
  });
  const publishReview = usePublishApiDesignReview({
    mutation: {
      onSuccess: () => void detailQuery.refetch(),
    },
  });
  const restoreRevision = useRestoreApiDesignRevision({
    mutation: {
      onSuccess: (response) => {
        if (response.status === 200) cacheDetail(response.data);
      },
    },
  });

  const activeReviewId = selectedReviewId ?? reviewId;
  const reviewFromServer = findReview(detail, activeReviewId);
  const review =
    reviewFromServer ??
    (optimisticReview !== null && optimisticReview.id === activeReviewId ? optimisticReview : null);
  const reviewStale = detail !== null && review !== null && review.revisionId !== detail.draft.id;
  const defaultBaselineId = defaultBaselineRevisionId(detail);
  const diffParams =
    detail === null
      ? undefined
      : selectedRevisionId !== null
        ? {
            fromRevisionId: selectedRevisionId,
            toRevisionId: comparisonToRevisionId ?? detail.draft.id,
          }
        : review !== null
          ? { fromRevisionId: review.baseRevisionId, toRevisionId: review.revisionId }
          : { fromRevisionId: defaultBaselineId, toRevisionId: detail.draft.id };
  const diffQuery = useGetApiDesignDiff(id, diffParams, {
    query: { enabled: detail !== null, retry: false },
  });
  const diff = diffQuery.data?.status === 200 ? diffQuery.data.data : null;
  const revisionQuery = useGetApiDesignRevision(id, selectedRevisionId ?? 0, {
    query: { enabled: selectedRevisionId !== null, retry: false },
  });

  const parsed = useMemo(() => parseDocument(buffer), [buffer]);
  const unsafeNumber = useMemo(() => hasUnsafeJsonNumber(buffer), [buffer]);
  const operations = parsed.document === null ? [] : listOperations(parsed.document);
  const schemas = parsed.document === null ? [] : listSchemas(parsed.document);
  const changedPointers = useMemo(
    () => new Set((diff?.changes ?? []).map((change) => change.pointer)),
    [diff],
  );
  const shownOperations = operations.filter((operation) => {
    const haystack = `${operation.method} ${operation.path}`.toLowerCase();
    const pointer = `/paths/${escapePointer(operation.path)}/${operation.method}`;
    return (
      haystack.includes(search.toLowerCase()) &&
      (!changedOnly || [...changedPointers].some((candidate) => candidate.startsWith(pointer)))
    );
  });
  const shownSchemas = schemas.filter((name) => {
    const pointer = `/components/schemas/${escapePointer(name)}`;
    return (
      name.toLowerCase().includes(search.toLowerCase()) &&
      (!changedOnly || [...changedPointers].some((candidate) => candidate.startsWith(pointer)))
    );
  });

  if (detailQuery.isError) {
    return (
      <Alert color="red" role="alert">
        {describeApiFailureDetailed(detailQuery.error)}
      </Alert>
    );
  }
  if (detailQuery.isPending || detail === null) {
    return <Loader aria-label="Загружаем проект API" />;
  }

  const selectedOperation =
    parsed.document !== null && selection.kind === "operation"
      ? getOperation(parsed.document, selection)
      : undefined;
  const selectedSchema =
    parsed.document !== null && selection.kind === "schema"
      ? getSchema(parsed.document, selection.name)
      : undefined;
  const candidateVersion =
    review === null ? null : revisionVersion(detail.revisions, review.revisionId);
  const compareOriginal =
    externalDetail !== null
      ? externalDetail.draft.document
      : (diff?.from.document ??
        (revisionQuery.data?.status === 200
          ? revisionQuery.data.data.document
          : (detail.published?.document ?? detail.draft.document)));
  const compareModified =
    externalDetail !== null ? buffer : (diff?.to.document ?? detail.draft.document);

  const treeContent = (
    <ApiTree
      composer={treeComposer}
      search={search}
      onSearch={setSearch}
      changedOnly={changedOnly}
      onChangedOnly={setChangedOnly}
      operations={shownOperations}
      schemas={shownSchemas}
      selection={selection}
      changedPointers={changedPointers}
      document={unsafeNumber ? null : parsed.document}
      onDocumentChange={(next, nextSelection) => {
        setBuffer(JSON.stringify(next, null, 2));
        setSelection(nextSelection);
        setView("editor");
        setEditorMode("form");
        tree.close();
      }}
      onRemoveDraftTree={draftStore.removeTree}
      onSelect={(next) => {
        setSelection(next);
        tree.close();
      }}
    />
  );
  const inspectorContent = (
    <Inspector
      view={inspectorView}
      onView={setInspectorView}
      changes={diff?.changes ?? []}
      revisions={detail.revisions}
      changeSets={detail.changeSets}
      selectedRevisionId={selectedRevisionId}
      onRevision={(fromRevisionId, toRevisionId) => {
        setSelectedRevisionId(fromRevisionId);
        setComparisonToRevisionId(toRevisionId ?? detail.draft.id);
        setView("compare");
        inspector.close();
      }}
      diagnostics={validatedDocument === buffer ? diagnostics : []}
      validated={validatedDocument === buffer}
      onChange={(change) => {
        setFocusPointer(change.pointer);
        selectPointer(change.pointer, setSelection);
        setView("compare");
        inspector.close();
      }}
      onValidate={() => validate.mutate({ id, data: { document: buffer } })}
      validating={validate.isPending}
      changeSetTitle={changeSetTitle}
      onChangeSetTitle={setChangeSetTitle}
      activeChangeSetId={activeChangeSetId}
      onCreateChangeSet={() =>
        createChangeSet.mutate({
          id,
          data: { expectedVersion: detail.design.version, title: changeSetTitle },
        })
      }
      onCloseChangeSet={() => {
        if (activeChangeSetId !== null) {
          closeChangeSet.mutate({
            id,
            cid: activeChangeSetId,
            data: { expectedVersion: detail.design.version },
          });
        }
      }}
      mutationError={
        validate.error ?? createChangeSet.error ?? closeChangeSet.error ?? restoreRevision.error
      }
      onRestore={() => {
        if (selectedRevisionId !== null) {
          restoreRevision.mutate({
            id,
            data: {
              expectedVersion: detail.design.version,
              revisionId: selectedRevisionId,
              summary: `Возврат к версии ${selectedRevisionId}`,
            },
          });
        }
      }}
      restorePending={restoreRevision.isPending}
      mock={
        <MockTester
          draftUrl={detail.design.draftUrl}
          publishedUrl={detail.design.publishedUrl}
          published={detail.design.publishedRevisionId !== null}
          initialMethod={selection.kind === "operation" ? selection.method : "GET"}
          initialPath={selection.kind === "operation" ? selection.path : "/"}
        />
      }
    />
  );

  return (
    <div className={classes.page} data-testid="api-designer-workbench">
      <Group justify="space-between" align="flex-start" className={classes.topbar}>
        <Group align="flex-start" wrap="nowrap">
          <Tooltip label="К проектам API">
            <Button
              variant="subtle"
              color="gray"
              px={7}
              aria-label="К проектам API"
              onClick={() => void navigate({ to: "/designs" })}
            >
              <IconChevronLeft size={20} />
            </Button>
          </Tooltip>
          <div>
            <Text className={classes.eyebrow}>Проектирование API</Text>
            <Title order={1} className={classes.projectTitle}>
              {detail.design.name}
            </Title>
            <div className={classes.versionStrip}>
              <span>Черновик · версия {detail.design.version}</span>
              <span>
                {detail.published === null
                  ? "Публикаций ещё нет"
                  : `Опубликована версия ${detail.published.version}`}
              </span>
              {dirty ? <Badge color="yellow">Есть несохранённые изменения</Badge> : null}
              {unsafeNumber ? (
                <Badge color="orange">Редактирование только в исходнике</Badge>
              ) : null}
              {detail.draft.source === "mcp" ? <Badge color="grape">Источник: MCP</Badge> : null}
            </div>
          </div>
        </Group>
        <Group gap="xs" justify="flex-end">
          <Button
            variant="light"
            leftSection={<IconSend size={16} />}
            disabled={dirty || parsed.error !== null}
            onClick={() => {
              setSelectedRevisionId(null);
              setComparisonToRevisionId(null);
              setView("review");
            }}
          >
            На проверку
          </Button>
        </Group>
      </Group>

      <details className={classes.mockAddresses}>
        <summary>Адреса моков</summary>
        <div className={classes.urlStrip} aria-label="Адреса моков">
          <div className={classes.urlCell}>
            <Text size="xs" c="dimmed">
              Мок черновика · версия {detail.design.version}
            </Text>
            <span className={classes.url} title={detail.design.draftUrl}>
              {detail.design.draftUrl}
            </span>
          </div>
          <div className={classes.urlCell}>
            <Text size="xs" c="dimmed">
              Опубликованный мок ·{" "}
              {detail.published === null
                ? "не опубликован"
                : `выпуск ${detail.releases.at(-1)?.number ?? 1}`}
            </Text>
            <span className={classes.url} title={detail.design.publishedUrl}>
              {detail.published === null
                ? "Станет доступен после публикации"
                : detail.design.publishedUrl}
            </span>
          </div>
        </div>
      </details>

      {externalDetail !== null ? (
        <Alert
          mt="md"
          color="yellow"
          icon={<IconAlertTriangle size={18} />}
          title={`На сервере появилась версия ${externalDetail.design.version}`}
          className={classes.conflict}
        >
          <Group justify="space-between" align="center">
            <Text size="sm">
              Локальный буфер сохранён. Сравните его с серверной версией или загрузите серверный
              черновик явно.
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" onClick={() => setView("compare")}>
                Сравнить
              </Button>
              <Button size="xs" color="yellow" onClick={() => adoptServer(externalDetail)}>
                Загрузить серверную версию
              </Button>
            </Group>
          </Group>
        </Alert>
      ) : null}
      {conflict ? (
        <Alert mt="md" color="red" role="alert" title="Конфликт версий">
          Черновик изменился на сервере. Сравните версии и решите конфликт вручную.
        </Alert>
      ) : null}

      <div className={classes.workbenchTools} aria-label="Инструменты проекта">
        <Group gap="xs" wrap="nowrap" className={classes.layoutTools}>
          {narrow || focusMode ? (
            <Button
              size="xs"
              variant="default"
              leftSection={<IconMenu2 size={16} />}
              aria-label="Открыть дерево API"
              aria-expanded={treeOpened}
              aria-controls="api-designer-tree-drawer"
              onClick={tree.open}
            >
              Структура API
            </Button>
          ) : null}
          {!narrow ? (
            <Button
              size="xs"
              variant={focusMode ? "light" : "subtle"}
              leftSection={focusMode ? <IconMinimize size={16} /> : <IconMaximize size={16} />}
              aria-pressed={focusMode}
              onClick={() => setFocusMode((current) => !current)}
            >
              Только редактор
            </Button>
          ) : null}
        </Group>
        <Group gap={4} wrap="nowrap" className={classes.inspectorTools}>
          {(
            [
              ["changes", "Изменения", IconGitCompare],
              ["history", "История", IconHistory],
              ["checks", "Проверки", IconListDetails],
              ["mock", "Мок", IconTestPipe],
            ] as const
          ).map(([section, label, Icon]) => (
            <Button
              key={section}
              size="xs"
              variant={inspectorOpened && inspectorView === section ? "light" : "subtle"}
              leftSection={<Icon size={16} />}
              rightSection={section === "changes" ? <span>{diff?.changes.length ?? 0}</span> : null}
              aria-label={label}
              aria-haspopup="dialog"
              aria-expanded={inspectorOpened && inspectorView === section}
              aria-controls="api-designer-inspector"
              onClick={() => {
                setInspectorView(section);
                inspector.open();
              }}
            >
              {label}
            </Button>
          ))}
        </Group>
      </div>

      <div className={classes.workbench} data-focused={focusMode || undefined}>
        <aside className={classes.tree} aria-label="Дерево API" hidden={narrow || focusMode}>
          <Text fw={650} size="sm" mb="md">
            Структура API
          </Text>
          {treeContent}
        </aside>
        <main className={classes.canvas}>
          <Tabs
            value={view}
            onChange={(next) => {
              const nextView = (next ?? "documentation") as CanvasView;
              if (nextView === "review") {
                setSelectedRevisionId(null);
                setComparisonToRevisionId(null);
              }
              setView(nextView);
            }}
            className={classes.canvasTabs}
          >
            <Tabs.List className={classes.tabList}>
              <Tabs.Tab value="documentation">Документация</Tabs.Tab>
              <Tabs.Tab value="editor">Редактор</Tabs.Tab>
              <Tabs.Tab value="compare">Сравнение</Tabs.Tab>
              <Tabs.Tab value="review">Проверка</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="documentation" className={classes.canvasPanel}>
              <Documentation
                document={parsed.document}
                selection={selection}
                operation={selectedOperation}
                schema={selectedSchema}
              />
            </Tabs.Panel>
            <Tabs.Panel value="editor" className={classes.canvasPanel}>
              <Stack gap="sm">
                <Group justify="space-between" align="flex-end">
                  <SegmentedControl
                    value={editorMode}
                    onChange={(next) => setEditorMode(next as EditorMode)}
                    data={[
                      { value: "form", label: "Форма", disabled: unsafeNumber },
                      { value: "source", label: "Исходник" },
                    ]}
                    aria-label="Режим редактора"
                  />
                  <Text
                    size="xs"
                    c={parsed.error === null && !formDraft.invalid ? "dimmed" : "red"}
                    component="output"
                  >
                    {parsed.error ??
                      (formDraft.invalid
                        ? "Исправьте JSON в полях формы"
                        : formDraft.dirty
                          ? "Завершите редактирование полей формы"
                          : dirty
                            ? `Локальный буфер от версии ${baseVersion}`
                            : "Сохранено")}
                  </Text>
                </Group>
                <div className={classes.editorFrame} data-mode={editorMode}>
                  {editorMode === "form" && unsafeNumber ? (
                    <Alert color="yellow" title="Число нельзя безопасно представить в JavaScript">
                      Документ содержит число, которое JavaScript не может сохранить без потери
                      точности. Используйте исходник, чтобы форма не округлила его при обновлении
                      соседнего поля.
                      <Button
                        size="xs"
                        variant="light"
                        mt="sm"
                        onClick={() => setEditorMode("source")}
                      >
                        Открыть исходник
                      </Button>
                    </Alert>
                  ) : editorMode === "form" && parsed.document !== null ? (
                    <DocumentForm
                      document={parsed.document}
                      selection={selection}
                      draftStore={draftStore}
                      onChange={(next) => setBuffer(JSON.stringify(next, null, 2))}
                    />
                  ) : editorMode === "form" ? (
                    <Alert color="yellow">
                      Исправьте синтаксис в исходнике. Невалидный локальный буфер не заменяет
                      последний серверный черновик.
                    </Alert>
                  ) : (
                    <SourceEditor value={buffer} onChange={setBuffer} />
                  )}
                </div>
                <TextInput
                  label="Описание изменения"
                  value={summary}
                  onChange={(event) => setSummary(event.currentTarget.value)}
                  placeholder="Например: добавлен фильтр статуса"
                />
                <Group justify="space-between">
                  <Button
                    variant="default"
                    leftSection={<IconTestPipe size={16} />}
                    loading={validate.isPending}
                    disabled={parsed.error !== null || formDraft.dirty}
                    onClick={() => validate.mutate({ id, data: { document: buffer } })}
                  >
                    Проверить
                  </Button>
                  <Button
                    leftSection={<IconDeviceFloppy size={16} />}
                    loading={save.isPending}
                    disabled={
                      !dirty || summary.trim() === "" || parsed.error !== null || formDraft.dirty
                    }
                    onClick={() => {
                      submittedFormDrafts.current = draftStore.serialize();
                      save.mutate({
                        id,
                        data: {
                          expectedVersion: baseVersion,
                          document: buffer,
                          summary: summary.trim(),
                          ...(activeChangeSetId === null ? {} : { changeSetId: activeChangeSetId }),
                        },
                      });
                    }}
                  >
                    Сохранить черновик
                  </Button>
                </Group>
                {save.isError && !conflict ? (
                  <Alert color="red" role="alert">
                    {describeApiFailureDetailed(save.error)}
                  </Alert>
                ) : null}
              </Stack>
            </Tabs.Panel>
            <Tabs.Panel value="compare" className={classes.canvasPanel}>
              <Stack gap="sm">
                <Group justify="space-between">
                  <div>
                    <Text fw={650}>Было / стало</Text>
                    <Text size="xs" c="dimmed">
                      {externalDetail !== null
                        ? `Серверная версия ${externalDetail.design.version} и ваш локальный буфер`
                        : selectedRevisionId !== null
                          ? `Сравнение revisions ${selectedRevisionId} → ${comparisonToRevisionId ?? detail.draft.id}`
                          : review !== null
                            ? `Зафиксированный кандидат · revision ${review.revisionId}`
                            : `База revision ${defaultBaselineId} и черновик revision ${detail.draft.id}`}
                    </Text>
                  </div>
                  <Button
                    size="xs"
                    variant="subtle"
                    leftSection={<IconRefresh size={14} />}
                    onClick={() => void diffQuery.refetch()}
                  >
                    Обновить diff
                  </Button>
                </Group>
                {diffQuery.isError ? (
                  <Alert color="red">{describeApiFailureDetailed(diffQuery.error)}</Alert>
                ) : null}
                <div className={classes.editorFrame}>
                  <SourceDiff
                    original={compareOriginal}
                    modified={compareModified}
                    narrow={narrow}
                    focusPointer={focusPointer}
                  />
                </div>
              </Stack>
            </Tabs.Panel>
            <Tabs.Panel value="review" className={classes.canvasPanel}>
              <ReviewPanel
                detail={detail}
                review={review}
                reviewStale={reviewStale}
                candidateVersion={candidateVersion}
                summary={reviewSummary}
                onSummary={setReviewSummary}
                onRequest={() =>
                  requestReview.mutate({
                    id,
                    data: { expectedVersion: detail.design.version, summary: reviewSummary.trim() },
                  })
                }
                requestDisabled={dirty || parsed.error !== null}
                requesting={requestReview.isPending}
                onPublish={() => {
                  if (review !== null) {
                    publishReview.mutate({
                      id,
                      rid: review.id,
                      data: { expectedVersion: detail.design.version },
                    });
                  }
                }}
                publishing={publishReview.isPending}
                error={requestReview.error ?? publishReview.error}
              />
            </Tabs.Panel>
          </Tabs>
        </main>
      </div>

      <Drawer
        opened={treeOpened}
        onClose={tree.close}
        title="Дерево API"
        position="left"
        size={300}
        id="api-designer-tree-drawer"
        closeButtonProps={{ "aria-label": "Закрыть дерево API" }}
      >
        {treeContent}
      </Drawer>
      <Drawer
        opened={inspectorOpened}
        onClose={inspector.close}
        title="Инспектор"
        position="right"
        size={440}
        id="api-designer-inspector"
        keepMounted
        closeButtonProps={{ "aria-label": "Закрыть инспектор" }}
      >
        {inspectorContent}
      </Drawer>
    </div>
  );
}

function useApiTreeComposer() {
  const [operationComposer, setOperationComposer] = useState<"create" | "copy" | null>(null);
  const [operationPath, setOperationPath] = useState("/");
  const [operationMethod, setOperationMethod] = useState("get");
  const [schemaComposer, setSchemaComposer] = useState(false);
  const [schemaName, setSchemaName] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);

  return {
    operationComposer,
    setOperationComposer,
    operationPath,
    setOperationPath,
    operationMethod,
    setOperationMethod,
    schemaComposer,
    setSchemaComposer,
    schemaName,
    setSchemaName,
    actionError,
    setActionError,
  };
}

function ApiTree({
  composer,
  search,
  onSearch,
  changedOnly,
  onChangedOnly,
  operations,
  schemas,
  selection,
  changedPointers,
  document,
  onDocumentChange,
  onRemoveDraftTree,
  onSelect,
}: {
  composer: ReturnType<typeof useApiTreeComposer>;
  search: string;
  onSearch: (value: string) => void;
  changedOnly: boolean;
  onChangedOnly: (value: boolean) => void;
  operations: Array<{ path: string; method: string }>;
  schemas: string[];
  selection: DocumentSelection;
  changedPointers: Set<string>;
  document: ApiDocument | null;
  onDocumentChange: (document: ApiDocument, selection: DocumentSelection) => void;
  onRemoveDraftTree: (pointer: string) => void;
  onSelect: (selection: DocumentSelection) => void;
}): ReactElement {
  const {
    operationComposer,
    setOperationComposer,
    operationPath,
    setOperationPath,
    operationMethod,
    setOperationMethod,
    schemaComposer,
    setSchemaComposer,
    schemaName,
    setSchemaName,
    actionError,
    setActionError,
  } = composer;

  function submitOperation(): void {
    if (document === null || operationPath.trim() === "") return;
    const target = { path: normalizeApiPath(operationPath), method: operationMethod };
    try {
      const next =
        operationComposer === "copy" && selection.kind === "operation"
          ? copyOperationWithUniqueId(document, selection, target)
          : createOperation(document, target);
      onDocumentChange(next, { kind: "operation", ...target });
      setActionError(null);
      setOperationComposer(null);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "Не удалось изменить операцию");
    }
  }

  function removeOperation(operation: { path: string; method: string }): void {
    if (document === null) return;
    onRemoveDraftTree(operationPointer(operation));
    onDocumentChange(deleteOperation(document, operation), { kind: "document" });
    setActionError(null);
  }

  function submitSchema(): void {
    if (document === null || schemaName.trim() === "") return;
    const name = schemaName.trim();
    try {
      onDocumentChange(createSchema(document, name), { kind: "schema", name });
      setSchemaName("");
      setSchemaComposer(false);
      setActionError(null);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "Не удалось создать схему");
    }
  }

  function removeSchema(name: string): void {
    if (document === null) return;
    onRemoveDraftTree(schemaPointer(name));
    onDocumentChange(deleteSchema(document, name), { kind: "document" });
    setActionError(null);
  }

  return (
    <Stack gap="xs" className={classes.treeScroll}>
      <TextInput
        aria-label="Поиск по API"
        placeholder="Метод, путь, схема"
        leftSection={<IconSearch size={15} />}
        value={search}
        onChange={(event) => onSearch(event.currentTarget.value)}
      />
      <Switch
        size="xs"
        label="Только изменения"
        checked={changedOnly}
        onChange={(event) => onChangedOnly(event.currentTarget.checked)}
      />
      <UnstyledButton
        className={classes.treeRow}
        data-selected={selection.kind === "document" || undefined}
        onClick={() => onSelect({ kind: "document" })}
      >
        <IconBraces size={15} aria-hidden="true" />
        <span className={classes.treeRowText}>Документ OpenAPI</span>
      </UnstyledButton>
      <Group justify="space-between" gap="xs">
        <Text className={classes.sectionLabel}>Методы</Text>
        <ActionIcon
          variant="subtle"
          size="sm"
          aria-label="Добавить операцию"
          disabled={document === null}
          onClick={() => {
            setOperationComposer(operationComposer === "create" ? null : "create");
            setOperationPath("/");
            setOperationMethod("get");
            setActionError(null);
          }}
        >
          <IconPlus size={15} />
        </ActionIcon>
      </Group>
      {operationComposer !== null ? (
        <Stack gap={6} className={classes.treeComposer}>
          <TextInput
            size="xs"
            label={operationComposer === "copy" ? "Новый путь копии" : "Путь"}
            value={operationPath}
            onChange={(event) => setOperationPath(event.currentTarget.value)}
          />
          <NativeSelect
            size="xs"
            label="Метод"
            value={operationMethod}
            onChange={(event) => setOperationMethod(event.currentTarget.value)}
            data={["get", "post", "put", "patch", "delete", "head", "options", "trace"]}
          />
          <Button
            size="compact-xs"
            disabled={operationPath.trim() === ""}
            onClick={submitOperation}
          >
            {operationComposer === "copy" ? "Создать копию" : "Создать операцию"}
          </Button>
        </Stack>
      ) : null}
      {operations.map((operation) => {
        const pointer = `/paths/${escapePointer(operation.path)}/${operation.method}`;
        const changed = [...changedPointers].some((candidate) => candidate.startsWith(pointer));
        const selected =
          selection.kind === "operation" &&
          selection.path === operation.path &&
          selection.method === operation.method;
        return (
          <div key={`${operation.method}:${operation.path}`} className={classes.treeActionRow}>
            <UnstyledButton
              className={classes.treeRow}
              data-selected={selected || undefined}
              onClick={() => onSelect({ kind: "operation", ...operation })}
            >
              <Badge color={methodColor(operation.method)} className={classes.method}>
                {operation.method.toUpperCase()}
              </Badge>
              <span className={classes.treeRowText}>{operation.path}</span>
              {changed ? <span className={classes.changeMark}>изменено</span> : null}
            </UnstyledButton>
            {selected ? (
              <Group gap={2} wrap="nowrap" className={classes.treeActions}>
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label={`Копировать ${operation.method.toUpperCase()} ${operation.path}`}
                  onClick={() => {
                    setOperationComposer("copy");
                    setOperationPath(operation.path);
                    setOperationMethod(operation.method);
                    setActionError(null);
                  }}
                >
                  <IconCopy size={14} />
                </ActionIcon>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  size="sm"
                  aria-label={`Удалить ${operation.method.toUpperCase()} ${operation.path}`}
                  onClick={() => removeOperation(operation)}
                >
                  <IconTrash size={14} />
                </ActionIcon>
              </Group>
            ) : null}
          </div>
        );
      })}
      {operations.length === 0 ? (
        <Text size="xs" c="dimmed" px={8}>
          Нет подходящих методов
        </Text>
      ) : null}
      <Group justify="space-between" gap="xs">
        <Text className={classes.sectionLabel}>Общие схемы</Text>
        <ActionIcon
          variant="subtle"
          size="sm"
          aria-label="Добавить схему"
          disabled={document === null}
          onClick={() => {
            setSchemaComposer((opened) => !opened);
            setActionError(null);
          }}
        >
          <IconPlus size={15} />
        </ActionIcon>
      </Group>
      {schemaComposer ? (
        <Stack gap={6} className={classes.treeComposer}>
          <TextInput
            size="xs"
            label="Название схемы"
            value={schemaName}
            onChange={(event) => setSchemaName(event.currentTarget.value)}
          />
          <Button size="compact-xs" disabled={schemaName.trim() === ""} onClick={submitSchema}>
            Создать схему
          </Button>
        </Stack>
      ) : null}
      {schemas.map((name) => {
        const pointer = `/components/schemas/${escapePointer(name)}`;
        const changed = [...changedPointers].some((candidate) => candidate.startsWith(pointer));
        return (
          <div key={name} className={classes.treeActionRow}>
            <UnstyledButton
              className={classes.treeRow}
              data-selected={
                selection.kind === "schema" && selection.name === name ? true : undefined
              }
              onClick={() => onSelect({ kind: "schema", name })}
            >
              <IconBraces size={15} aria-hidden="true" />
              <span className={classes.treeRowText}>{name}</span>
              {changed ? <span className={classes.changeMark}>изменено</span> : null}
            </UnstyledButton>
            {selection.kind === "schema" && selection.name === name ? (
              <ActionIcon
                variant="subtle"
                color="red"
                size="sm"
                aria-label={`Удалить схему ${name}`}
                className={classes.treeActions}
                onClick={() => removeSchema(name)}
              >
                <IconTrash size={14} />
              </ActionIcon>
            ) : null}
          </div>
        );
      })}
      {actionError !== null ? (
        <Alert color="red" p="xs" role="alert">
          {actionError}
        </Alert>
      ) : null}
    </Stack>
  );
}

function Documentation({
  document,
  selection,
  operation,
  schema,
}: {
  document: ApiDocument | null;
  selection: DocumentSelection;
  operation?: Record<string, unknown>;
  schema?: Record<string, unknown>;
}): ReactElement {
  if (document === null) {
    return <Alert color="yellow">Документация недоступна, пока в исходнике ошибка JSON.</Alert>;
  }
  if (selection.kind === "operation" && operation !== undefined) {
    const responses = isRecord(operation.responses) ? Object.keys(operation.responses) : [];
    const parameters = Array.isArray(operation.parameters) ? operation.parameters : [];
    return (
      <Stack className={classes.documentation}>
        <div className={classes.signature}>
          {selection.method.toUpperCase()} {selection.path}
        </div>
        <Title order={2}>
          {String(operation.summary ?? operation.operationId ?? selection.path)}
        </Title>
        <Text c="dimmed">{String(operation.description ?? "Описание пока не заполнено.")}</Text>
        <Group>
          <Badge color="gray">{parameters.length} параметров</Badge>
          <Badge color="gray">Ответы: {responses.join(", ") || "не заданы"}</Badge>
          {operation.deprecated === true ? <Badge color="orange">устарел</Badge> : null}
        </Group>
        <Title order={3}>Фрагмент контракта</Title>
        <Code block>{JSON.stringify(operation, null, 2)}</Code>
      </Stack>
    );
  }
  if (selection.kind === "schema" && schema !== undefined) {
    return (
      <Stack className={classes.documentation}>
        <Text className={classes.eyebrow}>Общая схема</Text>
        <Title order={2}>{selection.name}</Title>
        <Text c="dimmed">{String(schema.description ?? "Описание пока не заполнено.")}</Text>
        <Code block>{JSON.stringify(schema, null, 2)}</Code>
      </Stack>
    );
  }
  const info = isRecord(document.info) ? document.info : {};
  return (
    <Stack className={classes.documentation}>
      <Text className={classes.eyebrow}>OpenAPI {String(document.openapi ?? "")}</Text>
      <Title order={2}>{String(info.title ?? "Без названия")}</Title>
      <Text c="dimmed">{String(info.description ?? "Описание API пока не заполнено.")}</Text>
      <Group>
        <Badge color="gray">Версия контракта: {String(info.version ?? "—")}</Badge>
        <Badge color="gray">Методов: {listOperations(document).length}</Badge>
        <Badge color="gray">Схем: {listSchemas(document).length}</Badge>
      </Group>
    </Stack>
  );
}

function Inspector({
  view,
  onView,
  changes,
  revisions,
  changeSets,
  selectedRevisionId,
  onRevision,
  diagnostics,
  validated,
  onChange,
  onValidate,
  validating,
  changeSetTitle,
  onChangeSetTitle,
  activeChangeSetId,
  onCreateChangeSet,
  onCloseChangeSet,
  mutationError,
  onRestore,
  restorePending,
  mock,
}: {
  view: InspectorView;
  onView: (view: InspectorView) => void;
  changes: ApiDesignChange[];
  revisions: ApiDesignRevisionSummary[];
  changeSets: ApiDesignChangeSet[];
  selectedRevisionId: number | null;
  onRevision: (fromRevisionId: number, toRevisionId?: number) => void;
  diagnostics: ApiDesignDiagnostic[];
  validated: boolean;
  onChange: (change: ApiDesignChange) => void;
  onValidate: () => void;
  validating: boolean;
  changeSetTitle: string;
  onChangeSetTitle: (title: string) => void;
  activeChangeSetId: number | null;
  onCreateChangeSet: () => void;
  onCloseChangeSet: () => void;
  mutationError: unknown;
  onRestore: () => void;
  restorePending: boolean;
  mock: ReactNode;
}): ReactElement {
  return (
    <Stack gap="sm" className={classes.inspectorScroll}>
      <SegmentedControl
        fullWidth
        size="xs"
        value={view}
        onChange={(next) => onView(next as InspectorView)}
        data={[
          { value: "changes", label: "Diff" },
          { value: "history", label: "История" },
          { value: "checks", label: "Проверки" },
          { value: "mock", label: "Мок" },
        ]}
        aria-label="Раздел инспектора"
      />
      {view === "changes" ? (
        <>
          <Group justify="space-between">
            <Text fw={650}>Изменения</Text>
            <Badge color="gray">{changes.length}</Badge>
          </Group>
          {changes.length === 0 ? (
            <Text size="xs" c="dimmed">
              Содержательных изменений нет.
            </Text>
          ) : null}
          {changes.map((change) => (
            <UnstyledButton
              key={`${change.kind}:${change.pointer}`}
              className={classes.changeRow}
              onClick={() => onChange(change)}
            >
              <Group gap={6} wrap="nowrap">
                <Badge color={impactColor(change.impact)}>{impactLabel(change.impact)}</Badge>
                <Text size="xs" fw={650}>
                  {kindLabel(change.kind)}
                </Text>
              </Group>
              <Text size="xs" mt={5}>
                {change.description}
              </Text>
              <Text size="xs" c="dimmed" ff="monospace" mt={3}>
                {change.pointer}
              </Text>
              {change.before !== undefined || change.after !== undefined ? (
                <div className={classes.changeValues}>
                  <span className={classes.changeValue}>Было: {shortValue(change.before)}</span>
                  <span className={classes.changeValue}>Стало: {shortValue(change.after)}</span>
                </div>
              ) : null}
            </UnstyledButton>
          ))}
        </>
      ) : null}
      {view === "history" ? (
        <>
          <Text fw={650}>Неизменяемые версии</Text>
          {revisions.map((revision) => (
            <UnstyledButton
              key={revision.id}
              className={classes.historyRow}
              onClick={() => onRevision(revision.id)}
            >
              <Group justify="space-between" wrap="nowrap">
                <Text size="xs" fw={650}>
                  Версия {revision.version}
                </Text>
                <Badge color={revision.source === "mcp" ? "grape" : "teal"}>
                  {revision.source.toUpperCase()}
                </Badge>
              </Group>
              <Text size="xs" mt={4}>
                {revision.summary}
              </Text>
              {revision.changeSetId !== null ? (
                <Text size="xs" c="dimmed">
                  Набор:{" "}
                  {changeSets.find((set) => set.id === revision.changeSetId)?.title ??
                    `#${revision.changeSetId}`}
                </Text>
              ) : null}
              <Text size="xs" c="dimmed" mt={3}>
                {formatTimestamp(revision.createdAt)}
              </Text>
            </UnstyledButton>
          ))}
          {changeSets.length > 0 ? (
            <>
              <Text fw={650} mt="sm">
                Наборы изменений
              </Text>
              {changeSets.map((set) => {
                const lastRevision = revisions
                  .filter((revision) => revision.changeSetId === set.id)
                  .sort((left, right) => right.version - left.version)[0];
                return (
                  <UnstyledButton
                    key={set.id}
                    className={classes.historyRow}
                    disabled={lastRevision === undefined}
                    onClick={() => {
                      if (lastRevision !== undefined) {
                        onRevision(set.baseRevisionId, lastRevision.id);
                      }
                    }}
                  >
                    <Group justify="space-between" wrap="nowrap">
                      <Text size="xs" fw={650}>
                        {set.title}
                      </Text>
                      <Badge color={set.status === "open" ? "blue" : "gray"}>
                        {set.status === "open" ? "открыт" : "завершён"}
                      </Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={3}>
                      revisions {set.baseRevisionId} → {lastRevision?.id ?? "нет сохранений"} ·
                      источник {set.source.toUpperCase()}
                    </Text>
                  </UnstyledButton>
                );
              })}
            </>
          ) : null}
          <Button
            variant="default"
            color="yellow"
            leftSection={<IconRestore size={15} />}
            disabled={selectedRevisionId === null}
            loading={restorePending}
            onClick={onRestore}
          >
            Восстановить как новый черновик
          </Button>
        </>
      ) : null}
      {view === "checks" ? (
        <>
          <Button
            variant="default"
            leftSection={<IconCheck size={15} />}
            loading={validating}
            onClick={onValidate}
          >
            Проверить документ
          </Button>
          {diagnostics.length === 0 ? (
            validated ? (
              <Alert color="teal">Ошибок проверки нет.</Alert>
            ) : (
              <Text size="xs" c="dimmed">
                Запустите проверку текущего документа.
              </Text>
            )
          ) : null}
          {diagnostics.map((diagnostic, index) => (
            <Alert
              key={`${diagnostic.pointer}:${index}`}
              color={diagnostic.severity === "error" ? "red" : "yellow"}
            >
              <Text size="xs" fw={650}>
                {diagnostic.message}
              </Text>
              <Text size="xs" ff="monospace">
                {diagnostic.pointer}
              </Text>
            </Alert>
          ))}
          <Text fw={650} mt="sm">
            Набор изменений
          </Text>
          {activeChangeSetId === null ? (
            <>
              <TextInput
                label="Название задачи"
                value={changeSetTitle}
                onChange={(event) => onChangeSetTitle(event.currentTarget.value)}
              />
              <Button disabled={changeSetTitle.trim() === ""} onClick={onCreateChangeSet}>
                Открыть набор
              </Button>
            </>
          ) : (
            <Alert color="blue">
              <Text size="xs">Активен набор #{activeChangeSetId}</Text>
              <Button size="xs" variant="light" mt="xs" onClick={onCloseChangeSet}>
                Завершить набор
              </Button>
            </Alert>
          )}
          {mutationError !== null && mutationError !== undefined ? (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(mutationError)}
            </Alert>
          ) : null}
        </>
      ) : null}
      {view === "mock" ? mock : null}
    </Stack>
  );
}

function ReviewPanel({
  detail,
  review,
  reviewStale,
  candidateVersion,
  summary,
  onSummary,
  onRequest,
  requestDisabled,
  requesting,
  onPublish,
  publishing,
  error,
}: {
  detail: ApiDesignDetail;
  review: ApiDesignReview | null;
  reviewStale: boolean;
  candidateVersion: number | null;
  summary: string;
  onSummary: (value: string) => void;
  onRequest: () => void;
  requestDisabled: boolean;
  requesting: boolean;
  onPublish: () => void;
  publishing: boolean;
  error: unknown;
}): ReactElement {
  if (review === null) {
    return (
      <Stack maw={680}>
        <Text className={classes.eyebrow}>Неизменяемый кандидат</Text>
        <Title order={2}>Зафиксировать версию {detail.design.version} для проверки</Title>
        <Text c="dimmed">
          Кандидат сохранит текущий документ и базу сравнения. Следующие правки черновика его не
          изменят.
        </Text>
        <TextInput
          label="Что нужно проверить"
          value={summary}
          onChange={(event) => onSummary(event.currentTarget.value)}
          placeholder="Например: новый фильтр и ответ 422"
        />
        <Button
          leftSection={<IconGitCompare size={16} />}
          disabled={summary.trim() === "" || requestDisabled}
          loading={requesting}
          onClick={onRequest}
        >
          Создать кандидат
        </Button>
        {error !== null ? <Alert color="red">{describeApiFailureDetailed(error)}</Alert> : null}
      </Stack>
    );
  }
  return (
    <Stack maw={680}>
      <Group justify="space-between">
        <div>
          <Text className={classes.eyebrow}>Кандидат #{review.id}</Text>
          <Title order={2}>{review.summary}</Title>
        </div>
        <Badge color={reviewStale ? "orange" : review.status === "published" ? "teal" : "blue"}>
          {reviewStale
            ? "Кандидат устарел"
            : review.status === "published"
              ? "Опубликован"
              : "Готов к публикации"}
        </Badge>
      </Group>
      <Alert color={reviewStale ? "yellow" : "teal"}>
        Эта проверка зафиксирована на версии {candidateVersion ?? review.revisionId}. Текущий
        черновик — версия {detail.design.version}.
      </Alert>
      <Text size="sm">
        Опубликованная версия: {detail.published === null ? "ещё нет" : detail.published.version}.
        Она остаётся доступна только для чтения до следующей публикации.
      </Text>
      <Group>
        <Badge color="gray">Источник: {review.source.toUpperCase()}</Badge>
        <Badge color="gray">База: revision {review.baseRevisionId}</Badge>
        <Badge color="gray">Кандидат: revision {review.revisionId}</Badge>
      </Group>
      <Text size="sm" c="dimmed">
        Публикация переключит стабильный мок ровно на этот документ. Это действие доступно только в
        интерфейсе аналитика.
      </Text>
      <Button
        leftSection={<IconSend size={16} />}
        disabled={reviewStale || review.status !== "pending"}
        loading={publishing}
        onClick={onPublish}
      >
        Опубликовать версию
      </Button>
      {reviewStale || review.status !== "pending" ? (
        <Stack gap="xs" mt="sm">
          <Title order={3}>Новый кандидат из версии {detail.design.version}</Title>
          <Text size="sm" c="dimmed">
            Зафиксируйте текущий сохранённый черновик и продолжите проверку с новой парой версий.
          </Text>
          <TextInput
            label="Что нужно проверить в новом кандидате"
            value={summary}
            onChange={(event) => onSummary(event.currentTarget.value)}
          />
          <Button
            variant="light"
            leftSection={<IconGitCompare size={16} />}
            disabled={summary.trim() === "" || requestDisabled}
            loading={requesting}
            onClick={onRequest}
          >
            Создать новый кандидат
          </Button>
        </Stack>
      ) : null}
      {error !== null ? (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(error)}
        </Alert>
      ) : null}
    </Stack>
  );
}

function parseDocument(text: string): { document: ApiDocument | null; error: string | null } {
  try {
    const value: unknown = JSON.parse(text);
    if (!isRecord(value)) return { document: null, error: "Корень OpenAPI должен быть объектом" };
    return { document: value, error: null };
  } catch (error) {
    return { document: null, error: error instanceof Error ? error.message : "Некорректный JSON" };
  }
}

function defaultBaselineRevisionId(detail: ApiDesignDetail | null): number | undefined {
  if (detail === null) return undefined;
  if (detail.published !== null) return detail.published.id;
  return detail.revisions.reduce(
    (earliest, revision) => (revision.version < earliest.version ? revision : earliest),
    detail.draft,
  ).id;
}

function normalizeApiPath(path: string): string {
  const trimmed = path.trim();
  return trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
}

function copyOperationWithUniqueId(
  document: ApiDocument,
  source: { path: string; method: string },
  target: { path: string; method: string },
): ApiDocument {
  let next = copyOperation(document, source, target);
  const sourceOperation = getOperation(document, source);
  if (typeof sourceOperation?.operationId !== "string" || sourceOperation.operationId === "") {
    return next;
  }
  const usedIds = new Set(
    listOperations(document)
      .map((operation) => getOperation(document, operation)?.operationId)
      .filter((value): value is string => typeof value === "string"),
  );
  const base = `${sourceOperation.operationId}Copy`;
  let candidate = base;
  let suffix = 2;
  while (usedIds.has(candidate)) {
    candidate = `${base}${suffix}`;
    suffix += 1;
  }
  next = updateOperation(next, target, (operation) => ({ ...operation, operationId: candidate }));
  return next;
}

function storageKey(id: number): string {
  return `mocker:api-design:${id}:draft`;
}

function readStoredDraft(id: number): StoredDraft | null {
  try {
    const raw = localStorage.getItem(storageKey(id));
    if (raw === null) return null;
    const value: unknown = JSON.parse(raw);
    if (!isRecord(value)) return null;
    if (
      typeof value.text !== "string" ||
      typeof value.baseDocument !== "string" ||
      typeof value.baseVersion !== "number" ||
      typeof value.baseRevisionId !== "number" ||
      typeof value.savedAt !== "number"
    )
      return null;
    return {
      text: value.text,
      baseDocument: value.baseDocument,
      baseVersion: value.baseVersion,
      baseRevisionId: value.baseRevisionId,
      formDrafts: typeof value.formDrafts === "string" ? value.formDrafts : "{}",
      savedAt: value.savedAt,
    };
  } catch {
    return null;
  }
}

function readDiagnostics(details: unknown): ApiDesignDiagnostic[] {
  if (!isRecord(details) || !Array.isArray(details.diagnostics)) return [];
  return details.diagnostics.filter(
    (item): item is ApiDesignDiagnostic =>
      isRecord(item) &&
      typeof item.pointer === "string" &&
      typeof item.message === "string" &&
      (item.severity === "error" || item.severity === "warning"),
  );
}

function findReview(detail: ApiDesignDetail | null, requested?: number): ApiDesignReview | null {
  if (detail === null) return null;
  if (requested !== undefined)
    return detail.reviews.find((review) => review.id === requested) ?? null;
  return detail.reviews.findLast((review) => review.status === "pending") ?? null;
}

function revisionVersion(revisions: ApiDesignRevisionSummary[], id: number): number | null {
  return revisions.find((revision) => revision.id === id)?.version ?? null;
}

function selectPointer(pointer: string, select: (selection: DocumentSelection) => void): void {
  const tokens = pointer
    .slice(1)
    .split("/")
    .map((token) => token.replaceAll("~1", "/").replaceAll("~0", "~"));
  if (tokens[0] === "paths" && tokens[1] !== undefined && tokens[2] !== undefined) {
    select({ kind: "operation", path: tokens[1], method: tokens[2] });
  } else if (tokens[0] === "components" && tokens[1] === "schemas" && tokens[2] !== undefined) {
    select({ kind: "schema", name: tokens[2] });
  }
}

function escapePointer(value: string): string {
  return value.replaceAll("~", "~0").replaceAll("/", "~1");
}

function methodColor(method: string): string {
  return (
    (
      { get: "blue", post: "teal", put: "orange", patch: "grape", delete: "red" } as Record<
        string,
        string
      >
    )[method] ?? "gray"
  );
}

function impactColor(impact: ApiDesignChange["impact"]): string {
  return impact === "breaking" ? "red" : impact === "compatible" ? "teal" : "yellow";
}

function impactLabel(impact: ApiDesignChange["impact"]): string {
  return impact === "breaking"
    ? "Ломающий"
    : impact === "compatible"
      ? "Совместимый"
      : "Нужна проверка";
}

function kindLabel(kind: ApiDesignChange["kind"]): string {
  return kind === "added" ? "Добавлено" : kind === "removed" ? "Удалено" : "Изменено";
}

function shortValue(value: unknown): string {
  if (value === undefined) return "—";
  const text = typeof value === "string" ? value : JSON.stringify(value);
  return text.length > 80 ? `${text.slice(0, 77)}…` : text;
}
