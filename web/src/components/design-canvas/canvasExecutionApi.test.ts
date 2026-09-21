import { afterEach, describe, expect, it, vi } from "vitest";
import { json, route } from "@/test/http";
import {
  executeScenarioStep,
  runScenario,
  listScenarioRuns,
  getScenarioRun,
  cancelScenarioRun,
} from "./canvasExecutionApi";
import { emptyCanvas } from "./canvasModel";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("scenario runs API", () => {
  const report = {
    id: "ui-run",
    scenarioId: 12,
    revisionId: 41,
    version: 2,
    name: "",
    source: "ui",
    status: "running",
    startedAt: 1000,
    document: emptyCanvas(),
    inputVariables: {},
    variables: {},
    steps: [],
  };

  it("starts once with the client run ID and saved revision, accepts both created and idempotent responses", async () => {
    const fetchMock = route({ "POST /api/design-scenarios/12/runs": () => json(202, report) });
    const signal = new AbortController().signal;
    const input = {
      runId: "ui-run",
      revisionId: 41,
      variables: { token: "variant" },
      name: "Гость",
    };
    expect(await runScenario(12, input, signal)).toEqual(report);
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(signal);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual(input);
    expect(fetchMock).toHaveBeenCalledOnce();
    route({
      "POST /api/design-scenarios/12/runs": () => json(200, { ...report, status: "passed" }),
    });
    expect((await runScenario(12, input, signal)).status).toBe("passed");
  });

  it("lists shared reports, reads exact assertion text and cancels only the requested run", async () => {
    const precise = {
      ...report,
      source: "mcp",
      steps: [
        {
          messageId: "step",
          status: "passed",
          assertions: [
            {
              pointer: "/id",
              expectedJson: "9007199254740993",
              actualJson: "9007199254740993",
              passed: true,
            },
          ],
        },
      ],
    };
    const fetchMock = route({
      "GET /api/design-scenarios/12/runs": () => json(200, { runs: [report] }),
      "GET /api/design-scenarios/12/runs/ui-run": () => json(200, precise),
      "POST /api/design-scenarios/12/runs/ui-run/cancel": () =>
        json(200, { ...report, status: "cancelled" }),
    });
    const signal = new AbortController().signal;
    expect(await listScenarioRuns(12, signal)).toEqual([report]);
    expect(await getScenarioRun(12, "ui-run", signal)).toEqual(precise);
    expect((await cancelScenarioRun(12, "ui-run", signal)).status).toBe("cancelled");
    const cancel = fetchMock.mock.calls.at(-1)!;
    expect(JSON.parse(String(cancel[1]?.body))).toEqual({});
    expect(cancel[1]?.keepalive).toBe(true);
    expect(cancel[1]?.signal).toBe(signal);
  });

  it("reports a busy scenario without retrying the creation mutation", async () => {
    const fetchMock = route({
      "POST /api/design-scenarios/12/runs": () =>
        json(409, { error: { code: "conflict", message: "Сценарий уже выполняется" } }),
    });
    await expect(
      runScenario(12, { runId: "attempt", revisionId: 41 }, new AbortController().signal),
    ).rejects.toThrow(/Сценарий уже выполняется/);
    expect(fetchMock).toHaveBeenCalledOnce();
  });
});

describe("executeScenarioStep", () => {
  const request = {
    revisionId: 41,
    messageId: "login",
    pathParams: {},
    query: {},
    headers: {},
    body: "",
  };

  it("uses the scenario endpoint and forwards cancellation while treating mock HTTP errors as data", async () => {
    const response = {
      scenarioRevisionId: 41,
      designId: 7,
      designRevisionId: 11,
      workspaceRevision: 3,
      method: "POST",
      path: "/auth/login",
      status: 401,
      headers: { "Content-Type": "application/json" },
      body: '{"error":"unauthorized"}',
      durationMs: 3,
    };
    const fetchMock = route({
      "POST /api/design-scenarios/12/execute-step": () => json(200, response),
    });
    const controller = new AbortController();
    expect(await executeScenarioStep(12, request, controller.signal)).toEqual(response);
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(controller.signal);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual(request);
  });

  it("translates transport errors for the run report", async () => {
    route({
      "POST /api/design-scenarios/12/execute-step": () =>
        json(400, {
          error: { code: "bad_request", message: "Контракт не связан с API" },
        }),
    });
    await expect(executeScenarioStep(12, request, new AbortController().signal)).rejects.toThrow(
      "Некорректные данные: Контракт не связан с API",
    );
  });
});
