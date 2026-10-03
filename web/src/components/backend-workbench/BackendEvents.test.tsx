import { afterEach, expect, it, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import { BackendEvents } from "./BackendEvents";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
const hash = "a".repeat(64);
const witness = {
  provenance: "source",
  nodeIds: ["producer", "message", "consumer"],
  edgeIds: ["emit", "delivery-a"],
  evidenceIds: ["evidence"],
  status: "current",
  limitations: [],
};
const dispatch = [{ handlerId: "handler", handlesEdgeId: "handles", flowIds: ["flow"], witness }];
const route = {
  references: {
    producerId: "producer",
    messageId: "message",
    consumerId: "consumer",
    channelId: "channel",
    emitsEdgeId: "emit",
    deliveryEdgeId: "delivery-a",
  },
  condition: { status: "known", value: "configured" },
  group: { status: "unknown", reason: "group env unavailable" },
  dispatch,
  related: [
    {
      kind: "retries",
      edgeId: "retry",
      consumerId: "consumer",
      channelId: "retry-channel",
      messageId: "message",
      reason: "retry declared",
      delay: { status: "known", value: "5s" },
      maxAttempts: { status: "unknown", reason: "attempts unknown" },
      witness,
    },
    {
      kind: "dead_letters",
      edgeId: "dlq",
      consumerId: "consumer",
      channelId: "dlq-channel",
      messageId: "message",
      reason: "DLQ declared",
      witness,
    },
  ],
  emitContext: {
    flowId: "producer-flow",
    transaction: { status: "known", transactionId: "tx" },
    controlWitness: { ...witness, edgeIds: ["precommit"] },
    limitations: ["precommit possible"],
  },
  witness,
};
const evidence = {
  id: "evidence",
  subjectId: "producer",
  externalKey: "proof",
  method: "static",
  status: "explicit",
  explanation: "source",
  source: {
    repositoryId: "repo",
    snapshotId: "snap",
    file: "events.go",
    contentHash: hash,
    startLine: 12,
    endLine: 19,
  },
};
function fixture() {
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (String(url).endsWith("/events/query")) {
      requests.push(body);
      const items =
        body.view === "jobs"
          ? [
              {
                kind: "job",
                job: {
                  references: { jobId: "job" },
                  trigger: {
                    kind: "cron",
                    expression: { status: "known", value: "*/5 * * * *" },
                    timezone: { status: "unknown", reason: "timezone unknown" },
                  },
                  dispatch,
                  witness,
                },
              },
            ]
          : body.view === "service_calls"
            ? [
                {
                  kind: "service_call",
                  serviceCall: {
                    references: {
                      callStepId: "call",
                      operationId: "remote-operation",
                      callsEdgeId: "calls",
                    },
                    dispatch,
                    witness,
                  },
                },
              ]
            : [
                { kind: "route", route },
                {
                  kind: "route",
                  route: {
                    ...route,
                    references: { ...route.references, deliveryEdgeId: "delivery-b" },
                  },
                },
                {
                  kind: "boundary",
                  boundary: {
                    view: "routes",
                    references: { consumerId: "orphan" },
                    reason: "producer source unavailable",
                    dispatch: [{ unresolvedTargetId: "unknown-handler", flowIds: [], witness }],
                    related: [],
                    witness,
                  },
                },
              ];
      return json(200, {
        projectId: "project",
        revisionId: "rev",
        semanticHash: hash,
        policy: "source-events-projection-v1",
        view: body.view,
        ...(body.seedNodeId ? { seedNodeId: body.seedNodeId } : {}),
        ...(body.serviceId ? { serviceId: body.serviceId } : {}),
        items,
        nextCursor: body.cursor ? "" : "next",
        complete: true,
        truncated: false,
        truncationReasons: [],
        limitations: [],
        totalEdgeCount: 9,
        examinedEdgeCount: 9,
        constructedItemCount: items.length,
        auxiliaryRecordCount: 6,
        limits: {
          maxExaminedEdges: 20000,
          maxItems: 5000,
          maxAuxiliaryRecords: 20000,
          maxWitnessRecords: 256,
          defaultPageSize: 50,
          maxPageSize: 100,
          scanPolicy: "complete-scan-admission",
        },
        coverage: {
          coverage: {
            status: "partial",
            knownObjects: 6,
            denominator: null,
            gaps: ["missing source"],
          },
          inventory: [],
          snapshots: [],
        },
      });
    }
    if (String(url).endsWith("/graph/query") && body.id)
      return json(
        200,
        body.recordType === "edges"
          ? {
              nodes: [],
              edges: [
                {
                  id: body.id,
                  kind: body.id === "emit" ? "emits" : "delivered_to",
                  from: body.id === "emit" ? "producer" : "channel",
                  to: body.id === "emit" ? "message" : "consumer",
                  attributes:
                    body.id === "emit" ? { channelId: "channel" } : { messageId: "message" },
                },
              ],
              nextCursor: "",
            }
          : {
              nodes: [
                {
                  id: body.id,
                  kind: body.id === "producer" ? "flow_step" : "consumer",
                  attributes: body.id === "producer" ? { stepKind: "emit" } : {},
                },
              ],
              edges: [],
              nextCursor: "",
            },
      );
    if (String(url).endsWith("/graph/query"))
      return json(200, {
        nodes: [
          {
            id: "field",
            kind: "event_field",
            parentId: "message",
            name: "Order ID",
            attributes: { section: "payload", path: [] },
            evidenceIds: [],
          },
        ],
        edges: [],
        nextCursor: "",
      });
    if (String(url).includes("/evidence"))
      return json(200, {
        items:
          new URL(String(url), "http://localhost").searchParams.get("subjectId") === "producer"
            ? [evidence]
            : [],
        nextCursor: "",
      });
    if (String(url).endsWith("/revisions/rev"))
      return json(200, { id: "rev", projectId: "project", schemaVersion: "5", semanticHash: hash });
    return json(200, { items: [], nextCursor: "" });
  });
  return requests;
}
afterEach(() => vi.unstubAllGlobals());
it("navigates exact forward and reverse routes and each handler Flow using the selected revision", async () => {
  const requests = fixture(),
    navigate = vi.fn();
  renderWithProviders(
    <BackendEvents
      projectId="project"
      revisionId="rev"
      semanticHash={hash}
      onFlowNavigate={navigate}
    />,
  );
  fireEvent.click((await screen.findAllByRole("button", { name: "Открыть Flow flow" }))[0]!);
  expect(navigate).toHaveBeenCalledWith({
    revisionId: "rev",
    entrypointId: "consumer",
    flowId: "flow",
    recordId: "handler",
    recordType: "node",
  });
  fireEvent.click(screen.getAllByRole("button", { name: "Маршруты получателя consumer" })[0]!);
  await waitFor(() =>
    expect(requests.at(-1)).toMatchObject({
      revisionId: "rev",
      seedNodeId: "consumer",
      cursor: "",
    }),
  );
  fireEvent.click(
    (await screen.findAllByRole("button", { name: "Маршруты отправителя producer" }))[0]!,
  );
  await waitFor(() =>
    expect(requests.at(-1)).toMatchObject({ seedNodeId: "producer", cursor: "" }),
  );
  expect((await screen.findAllByText(/group env unavailable/)).length).toBeGreaterThan(0);
  expect(screen.getAllByText(/DLQ declared/).length).toBeGreaterThan(0);
  expect(screen.getAllByText(/precommit possible/).length).toBeGreaterThan(0);
  expect(screen.getByText(/producer source unavailable/)).toBeVisible();
});
it("uses message membership plus endpoint and edge to seed distinct event values", async () => {
  fixture();
  renderWithProviders(<BackendEvents projectId="project" revisionId="rev" semanticHash={hash} />);
  fireEvent.click((await screen.findAllByRole("button", { name: "Поля сообщения message" }))[0]!);
  const a = await screen.findByRole("button", {
    name: /Открыть значение Order ID.*consumer.*delivery-a/,
  });
  fireEvent.click(a);
  expect(await screen.findByText(/Маршрут значения: delivery-a/)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Закрыть значение события" }));
  await waitFor(() => expect(a).toHaveFocus());
  fireEvent.click(screen.getAllByRole("button", { name: "Поля сообщения message" })[1]!);
  fireEvent.click(
    await screen.findByRole("button", { name: /Открыть значение Order ID.*consumer.*delivery-b/ }),
  );
  expect(await screen.findByText(/Маршрут значения: delivery-b/)).toBeVisible();
});
it("resets page and selectors when changing to jobs and service calls and retains static unknowns", async () => {
  const requests = fixture();
  renderWithProviders(<BackendEvents projectId="project" revisionId="rev" semanticHash={hash} />);
  fireEvent.click(await screen.findByRole("button", { name: "Следующие событий" }));
  await waitFor(() => expect(requests.at(-1)).toMatchObject({ cursor: "next" }));
  fireEvent.change(screen.getByLabelText("Представление событий"), { target: { value: "jobs" } });
  await screen.findByText(/timezone unknown/);
  expect(requests.at(-1)).toEqual({ revisionId: "rev", view: "jobs", limit: 50, cursor: "" });
  fireEvent.change(screen.getByLabelText("Представление событий"), {
    target: { value: "service_calls" },
  });
  expect(await screen.findByRole("button", { name: "Операция remote-operation" })).toBeVisible();
  expect(requests.at(-1)).toEqual({
    revisionId: "rev",
    view: "service_calls",
    limit: 50,
    cursor: "",
  });
});
it("exports only a captured fragment with physical source proof, question and criterion through reads", async () => {
  fixture();
  renderWithProviders(<BackendEvents projectId="project" revisionId="rev" semanticHash={hash} />);
  fireEvent.click(
    (await screen.findAllByRole("button", { name: "Подготовить исследование пробела" }))[2]!,
  );
  const exportButton = screen.getByRole("button", { name: "Собрать контекст исследования" });
  expect(exportButton).toBeDisabled();
  expect(screen.queryByLabelText("Загружаем оснований исследования")).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Вопрос исследования"), {
    target: { value: "Where is the handler?" },
  });
  fireEvent.change(screen.getByLabelText("Критерий завершения"), {
    target: { value: "Evidence or reason + inspected scope" },
  });
  fireEvent.click(exportButton);
  const text = await screen.findByLabelText("Контекст исследования JSON");
  const captured = JSON.parse(text.textContent!);
  expect(captured).toMatchObject({
    projectId: "project",
    revisionId: "rev",
    semanticHash: hash,
    question: "Where is the handler?",
    completionCriterion: "Evidence or reason + inspected scope",
    fragment: { kind: "boundary" },
  });
  expect(captured.evidence[0].source).toEqual(evidence.source);
  fireEvent.change(screen.getByLabelText("Представление событий"), { target: { value: "jobs" } });
  await screen.findByText(/timezone unknown/);
  expect(JSON.parse(screen.getByLabelText("Контекст исследования JSON").textContent!)).toEqual(
    captured,
  );
});
it("honors dirty editor departure guards when changing event selection", async () => {
  const requests = fixture();
  renderWithProviders(
    <BackendAPIArtifactsContext value={{ guard: () => false } as never}>
      <BackendEvents projectId="project" revisionId="rev" semanticHash={hash} />
    </BackendAPIArtifactsContext>,
  );
  fireEvent.click(
    (await screen.findAllByRole("button", { name: "Маршруты получателя consumer" }))[0]!,
  );
  expect(requests).toHaveLength(1);
});
