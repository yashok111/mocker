import { exactCoverage, exactEnvelope, exactIDs, exactSource } from "@/test/backendExact";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import type { ArtifactProjectionPage, BackendRevision } from "@/api/generated/schemas";
import { BackendArtifactProjections } from "./BackendArtifactProjections";
import { BackendAPIArtifactsContext } from "./BackendAPIArtifacts";
const hash = "a".repeat(64);
const pin = { kind: "design_scenario", id: "12", revisionId: "23", contentHash: hash };
const revision = {
  id: "0197aaf9-5555-7000-8000-000000000101",
  projectId: "project",
  semanticHash: hash,
  sourceSnapshotIds: ["snap"],
  artifactPins: [pin],
} as BackendRevision;
const binding = {
  artifactKind: "design_scenario" as const,
  artifactId: "12",
  selector: { kind: "participant" as const, participantId: "deleted" },
  sourceNodeIds: ["0197aaf9-5555-7000-8000-000000000119"],
  sourceLabels: ["Frozen source"],
  objectHash: hash,
  lastKnownLabel: "Deleted participant",
  origin: "manual" as const,
  reason: "0197aaf9-5555-7000-8000-000000000109",
};
const page: ArtifactProjectionPage = {
  revisionId: "0197aaf9-5555-7000-8000-000000000101",
  semanticHash: hash,
  sourceSnapshotIds: ["snap"],
  pins: [pin],
  selectedPin: pin,
  hashPolicy: "design-scenario-envelope-v1",
  view: "sequence",
  apiBindings: [],
  editorBindings: [binding],
  bindingsComplete: true,
  items: [
    {
      id: "p",
      kind: "participant",
      label: "Authored participant",
      locator: { pin, view: "sequence", owner: { pointer: "/participants/0" } },
      objectHash: hash,
      sourceNodeIds: [],
      data: {
        kind: "participant",
        participant: { id: "p", name: "Sender", kind: "service", description: "" },
      },
      bindingSelector: { kind: "participant", participantId: "p" },
      diagnostics: [],
    },
  ],
  nextCursor: "",
  resolution: { status: "resolved", diagnostics: [], updateAvailable: false },
  diagnostics: [],
  coverage: {
    itemsReturned: 1,
    totalItems: 1,
    nodesReturned: 1,
    edgesReturned: 0,
    diagnosticsReturned: 0,
    truncatedReasons: [],
  },
  complete: true,
};
afterEach(() => vi.unstubAllGlobals());
function mount() {
  const onApplied = vi.fn();
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{ revision, projectVersion: 4, canEdit: true, onDirty: vi.fn(), onApplied }}
    >
      <BackendArtifactProjections
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
      />
    </BackendAPIArtifactsContext>,
  );
  return onApplied;
}
it("preserves unrelated deleted roster and retries exact unknown body and key", async () => {
  const previews: unknown[] = [];
  const applies: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url, init) => {
      const path = String(url);
      if (path.endsWith("/query")) return json(200, page);
      if (path.endsWith("/preview")) {
        const body = JSON.parse(init.body);
        previews.push(body);
        return json(200, {
          ...body,
          candidateHash: hash,
          semanticHash: hash,
          pins: [pin],
          apiBindings: [],
          editorBindings: [binding],
          sourceSnapshotIds: ["snap"],
          diagnostics: [],
          diff: [],
          diffTruncated: false,
          canApply: true,
        });
      }
      if (path.endsWith("/commands")) {
        applies.push(init.body);
        if (applies.length === 1) throw new TypeError("lost response");
        return json(200, {
          project: { id: "project", version: 5 },
          revision: {
            ...revision,
            id: "0197aaf9-5555-7000-8000-000000000108",
            parentRevisionId: "0197aaf9-5555-7000-8000-000000000101",
          },
        });
      }
      throw new Error(path);
    }),
  );
  const onApplied = mount();
  const user = userEvent.setup();
  await screen.findByText("Authored participant · participant");
  await user.click(screen.getByRole("button", { name: "Изменить закреплённую группу" }));
  await user.type(screen.getByLabelText("Причина изменения"), "explicit intent");
  await user.type(
    screen.getByLabelText("Точные исходные узлы для выбранного объекта"),
    "0197aaf9-5555-7000-8000-000000000119",
  );
  await user.click(screen.getByRole("button", { name: "Связать выбранный объект" }));
  await user.click(screen.getByRole("button", { name: "Предпросмотр полной группы" }));
  await waitFor(() => expect(previews).toHaveLength(1));
  expect(previews[0]).toMatchObject({
    commands: [
      {
        editorBindings: [
          { selector: binding.selector, sourceNodeIds: ["0197aaf9-5555-7000-8000-000000000119"] },
          {
            selector: { kind: "participant", participantId: "p" },
            sourceNodeIds: ["0197aaf9-5555-7000-8000-000000000119"],
          },
        ],
      },
    ],
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить показанную группу" })).toBeEnabled(),
  );
  await user.click(screen.getByRole("button", { name: "Применить показанную группу" }));
  await screen.findByText(/Результат неизвестен/);
  await user.click(screen.getByRole("button", { name: "Повторить точное применение" }));
  await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(1));
  expect(applies).toHaveLength(2);
  expect(applies[0]).toBe(applies[1]);
});
it("full roster stays visible with filtered or unsupported empty content", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        json(200, {
          ...page,
          items: [],
          complete: false,
          resolution: { ...page.resolution, status: "unsupported" },
          coverage: { ...page.coverage, truncatedReasons: ["construction_bytes"] },
        }),
      ),
    ),
  );
  mount();
  await screen.findByText("Deleted participant");
  expect(screen.getByText(/Frozen source/)).toBeVisible();
  expect(screen.getByText(/construction_bytes/)).toBeVisible();
  expect(screen.getByRole("button", { name: "Точный сырой снимок" })).toBeEnabled();
});
it("checks all four view scopes while preserving the full frozen roster", async () => {
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, init) => {
      const input = JSON.parse(init.body);
      requests.push(input);
      return json(200, {
        ...page,
        view: input.view,
        embeddedContractId: input.embeddedContractId,
        items: [],
      });
    }),
  );
  mount();
  const user = userEvent.setup();
  await screen.findByText("Deleted participant");
  await user.selectOptions(screen.getByLabelText("Представление модели"), "event_model");
  await waitFor(() => expect(requests.at(-1)?.view).toBe("event_model"));
  await user.selectOptions(screen.getByLabelText("Представление модели"), "states");
  await user.type(screen.getByLabelText("Точный ID вложенного контракта"), "copy");
  await waitFor(() =>
    expect(requests.at(-1)).toMatchObject({ view: "states", embeddedContractId: "copy" }),
  );
  await user.selectOptions(screen.getByLabelText("Представление модели"), "response_rules");
  await waitFor(() =>
    expect(requests.at(-1)).toMatchObject({ view: "response_rules", embeddedContractId: "copy" }),
  );
  expect(screen.getByText("Deleted participant")).toBeVisible();
});
it("aborts query ownership on unmount", async () => {
  let signal: AbortSignal | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn((_url, init) => {
      signal = init.signal;
      return new Promise<Response>(() => {});
    }),
  );
  const rendered = renderWithProviders(
    <BackendAPIArtifactsContext
      value={{ revision, projectVersion: 4, canEdit: true, onDirty: vi.fn(), onApplied: vi.fn() }}
    >
      <BackendArtifactProjections
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
      />
    </BackendAPIArtifactsContext>,
  );
  await waitFor(() => expect(signal).toBeDefined());
  rendered.unmount();
  await waitFor(() => expect(signal?.aborted).toBe(true));
});
it("refuses a second editor instance claim", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(json(200, page))),
  );
  const claim = vi.fn().mockReturnValueOnce(true).mockReturnValue(false);
  renderWithProviders(
    <BackendAPIArtifactsContext
      value={{
        revision,
        projectVersion: 4,
        canEdit: true,
        onDirty: vi.fn(),
        onApplied: vi.fn(),
        claim,
      }}
    >
      <BackendArtifactProjections
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
      />
      <BackendArtifactProjections
        projectId="project"
        revisionId="0197aaf9-5555-7000-8000-000000000101"
      />
    </BackendAPIArtifactsContext>,
  );
  const triggers = await screen.findAllByRole("button", { name: "Изменить закреплённую группу" });
  await userEvent.click(triggers[0]!);
  await userEvent.click(triggers[1]!);
  expect(await screen.findByText(/Завершите изменение в другом инспекторе/)).toBeVisible();
  expect(screen.getAllByLabelText("Причина изменения")).toHaveLength(1);
});
it("allows paging during editing and keeps the complete frozen replacement vector", async () => {
  const previews: unknown[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url, init) => {
      const input = JSON.parse(init.body);
      if (String(url).endsWith("/preview")) {
        previews.push(input);
        return json(200, {
          ...input,
          candidateHash: hash,
          semanticHash: hash,
          pins: [pin],
          apiBindings: [],
          editorBindings: [binding],
          sourceSnapshotIds: ["snap"],
          diagnostics: [],
          diff: [],
          diffTruncated: false,
          canApply: false,
        });
      }
      return json(200, {
        ...page,
        nextCursor: input.cursor ? "" : "0197aaf9-5555-7000-8000-000000000108",
        items: input.cursor
          ? [
              {
                ...page.items[0],
                id: "q",
                label: "Second page",
                bindingSelector: { kind: "participant", participantId: "q" },
              },
            ]
          : page.items,
      });
    }),
  );
  mount();
  await screen.findByText("Authored participant · participant");
  await userEvent.click(screen.getByRole("button", { name: "Изменить закреплённую группу" }));
  await userEvent.type(screen.getByLabelText("Причина изменения"), "page intent");
  await userEvent.type(
    screen.getByLabelText("Точные исходные узлы для выбранного объекта"),
    "0197aaf9-5555-7000-8000-000000000119",
  );
  await userEvent.click(screen.getByRole("button", { name: "Следующие строки" }));
  await screen.findByText("Second page · participant");
  await userEvent.click(screen.getByRole("button", { name: "Связать выбранный объект" }));
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр полной группы" }));
  await waitFor(() => expect(previews).toHaveLength(1));
  expect(previews[0]).toMatchObject({
    commands: [
      {
        editorBindings: [
          { selector: binding.selector, sourceNodeIds: ["0197aaf9-5555-7000-8000-000000000119"] },
          {
            selector: { kind: "participant", participantId: "q" },
            sourceNodeIds: ["0197aaf9-5555-7000-8000-000000000119"],
          },
        ],
      },
    ],
  });
});
it("never infers source navigation from identical copy operation keys", async () => {
  const copies = ["first", "second"].map((id, i) => ({
    ...page.items[0]!,
    id,
    label: id,
    sourceNodeIds: [],
    locator: {
      pin,
      view: "sequence",
      owner: { pointer: `/contracts/${i}`, operationKey: "same-key" },
      embedded: {
        contractId: id,
        mode: "copy",
        documentHash: hash,
        origin: { designId: "9007199254740993", revisionId: "9007199254740995", version: "0" },
        originStatus: "copy",
      },
    },
    data: {
      kind: "api_operation",
      apiOperation: {
        operationKey: "same-key",
        method: "get",
        path: "/same",
        summary: id,
        documentJSON: "{}",
      },
    },
    bindingSelector: undefined,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(json(200, { ...page, items: copies }))),
  );
  mount();
  await screen.findByText("first · participant");
  expect(screen.queryByRole("button", { name: /Исходный узел/ })).not.toBeInTheDocument();
  expect(screen.getAllByText(/версия 0/)).toHaveLength(2);
});

it("does not adopt a late successful apply over a newer project head", async () => {
  let resolve!: (r: Response) => void;
  const onApplied = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url, init) => {
      if (String(url).endsWith("/query")) return json(200, page);
      if (String(url).endsWith("/preview"))
        return json(200, {
          ...JSON.parse(init.body),
          candidateHash: hash,
          semanticHash: hash,
          pins: [pin],
          apiBindings: [],
          editorBindings: [binding],
          sourceSnapshotIds: ["snap"],
          diagnostics: [],
          diff: [],
          diffTruncated: false,
          canApply: true,
        });
      if (String(url).endsWith("/commands")) return new Promise<Response>((r) => (resolve = r));
      throw new Error(String(url));
    }),
  );
  function Host() {
    const [version, setVersion] = useState(4);
    return (
      <>
        <button onClick={() => setVersion(9)}>Observe new head</button>
        <BackendAPIArtifactsContext
          value={{
            revision,
            projectVersion: version,
            canEdit: version === 4,
            onDirty: vi.fn(),
            onApplied,
          }}
        >
          <BackendArtifactProjections
            projectId="project"
            revisionId="0197aaf9-5555-7000-8000-000000000101"
          />
        </BackendAPIArtifactsContext>
      </>
    );
  }
  renderWithProviders(<Host />);
  await screen.findByText("Deleted participant");
  await userEvent.click(screen.getByRole("button", { name: "Изменить закреплённую группу" }));
  await userEvent.type(screen.getByLabelText("Причина изменения"), "intent");
  await userEvent.click(screen.getByRole("button", { name: "Предпросмотр полной группы" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Применить показанную группу" })).toBeEnabled(),
  );
  await userEvent.click(screen.getByRole("button", { name: "Применить показанную группу" }));
  await waitFor(() => expect(resolve).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Observe new head" }));
  resolve(
    json(200, {
      project: { id: "project", version: 5 },
      revision: {
        ...revision,
        id: "0197aaf9-5555-7000-8000-000000000108",
        parentRevisionId: "0197aaf9-5555-7000-8000-000000000101",
      },
    }),
  );
  await screen.findByText(/Применение подтверждено/);
  expect(onApplied).not.toHaveBeenCalled();
});
it.each([413, 422])(
  "blocks a rejected %s candidate until an explicit new preview",
  async (status) => {
    let applies = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url, init) => {
        if (String(url).endsWith("/query")) return json(200, page);
        if (String(url).endsWith("/preview"))
          return json(200, {
            ...JSON.parse(init.body),
            candidateHash: hash,
            semanticHash: hash,
            pins: [pin],
            apiBindings: [],
            editorBindings: [binding],
            sourceSnapshotIds: ["snap"],
            diagnostics: [],
            diff: [],
            diffTruncated: false,
            canApply: true,
          });
        if (String(url).endsWith("/commands")) {
          applies++;
          return json(status, {
            error: {
              code: "narrow_request",
              message: "Rejected candidate",
              details: { allowedBytes: 1024, reservedApplyBytes: 2048 },
            },
          });
        }
        throw new Error(String(url));
      }),
    );
    mount();
    await screen.findByText("Deleted participant");
    await userEvent.click(screen.getByRole("button", { name: "Изменить закреплённую группу" }));
    await userEvent.type(screen.getByLabelText("Причина изменения"), "intent");
    await userEvent.click(screen.getByRole("button", { name: "Предпросмотр полной группы" }));
    const apply = screen.getByRole("button", { name: "Применить показанную группу" });
    await waitFor(() => expect(apply).toBeEnabled());
    await userEvent.click(apply);
    await screen.findByText(/Rejected candidate/);
    expect(apply).toBeDisabled();
    expect(applies).toBe(1);
    expect(screen.queryByText(/Результат неизвестен/)).not.toBeInTheDocument();
  },
);

it.each([
  ["source", false],
  ["full", false],
  ["source", true],
  ["full", true],
] as const)(
  "loads embedded owner projection from a %s link; explicit context %s",
  async (kind, explicitContext) => {
    const target =
      kind === "full"
        ? {
            changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
          }
        : { revisionId: exactIDs.revision };
    const artifactPin = {
      kind: "design_scenario",
      id: "12",
      revisionId: "23",
      contentHash: "a".repeat(64),
    };
    const envelope = exactEnvelope(target);
    const pins = { ...envelope.pins, artifactPins: [artifactPin] };
    const artifactRequests: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url, init) => {
        const path = String(url);
        if (path.includes("/artifacts/query")) {
          artifactRequests.push(JSON.parse(String(init?.body)));
          return json(200, {
            target,
            effectivePins: pins,
            viewSchemaVersion: pins.viewSchemaVersion,
            revisionId: pins.baseRevisionId,
            semanticHash: pins.effectiveSemanticHash,
            sourceSnapshotIds: pins.sourceSnapshotIds,
            pins: [artifactPin],
            selectedPin: artifactPin,
            hashPolicy: "design-scenario-envelope-v1",
            view: "sequence",
            apiBindings: [],
            editorBindings: [],
            bindingsComplete: true,
            items: [],
            nextCursor: "",
            resolution: { status: "resolved", diagnostics: [], updateAvailable: false },
            diagnostics: [],
            coverage: {
              itemsReturned: 0,
              totalItems: 0,
              nodesReturned: 0,
              edgesReturned: 0,
              diagnosticsReturned: 0,
              truncatedReasons: [],
            },
            complete: true,
          });
        }
        if (path.includes("coverage"))
          return json(200, { ...envelope, pins, ...exactCoverage, source: exactSource() });
        return json(200, {
          id: exactIDs.revision,
          projectId: exactIDs.project,
          semanticHash: pins.effectiveSemanticHash,
          sourceSnapshotIds: pins.sourceSnapshotIds,
          artifactPins: [artifactPin],
          schemaVersion: "6",
        });
      }),
    );
    renderWithProviders(
      <BackendArtifactProjections
        projectId={exactIDs.project}
        target={target}
        {...(explicitContext
          ? kind === "full"
            ? { pins }
            : { revisionId: exactIDs.revision }
          : {})}
        readOnly
        initialArtifact="design_scenario:12"
        initialView="sequence"
      />,
    );
    await waitFor(() => expect(artifactRequests).toHaveLength(1));
    expect(artifactRequests[0]).toMatchObject(target);
  },
);

it.each(["source", "full"] as const)(
  "does not fall back to host/source metadata when %s owner context resolution fails",
  async (kind) => {
    const target =
      kind === "source"
        ? { revisionId: exactIDs.revision }
        : {
            changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
          };
    const requests: string[] = [];
    vi.stubGlobal("fetch", async (url: RequestInfo | URL) => {
      requests.push(String(url));
      return json(500, { error: { message: "Context unavailable" } });
    });
    renderWithProviders(
      <BackendAPIArtifactsContext
        value={{
          revision: {
            id: exactIDs.revision,
            projectId: exactIDs.project,
            semanticHash: hash,
            sourceSnapshotIds: [],
            artifactPins: [pin],
          } as never,
          projectVersion: 1,
          canEdit: true,
          onDirty: vi.fn(),
          onApplied: vi.fn(),
        }}
      >
        <BackendArtifactProjections
          projectId={exactIDs.project}
          target={target}
          readOnly
          initialArtifact="design_scenario:12"
          initialView="sequence"
        />
      </BackendAPIArtifactsContext>,
    );
    await screen.findByRole("button", { name: "Повторить загрузку контекста модели" });
    expect(requests).toHaveLength(1);
    expect(requests[0]).toContain("/coverage");
    expect(
      requests.some((url) => url.includes("/artifacts/query") || url.endsWith("/revisions/")),
    ).toBe(false);
  },
);

it("aborts old owner coverage when a full target changes and keeps the new immutable draft", async () => {
  const first = {
    changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.snapshot },
  };
  const second = {
    changeProposal: { proposalId: exactIDs.project, proposalRevisionId: exactIDs.repository },
  };
  const artifactPin = { kind: "design_scenario", id: "12", revisionId: "23", contentHash: hash };
  let oldSignal: AbortSignal | undefined;
  let finish!: (response: Response) => void;
  const requests: Record<string, unknown>[] = [];
  const envelope = exactEnvelope(second);
  const pins = { ...envelope.pins, artifactPins: [artifactPin] };
  vi.stubGlobal("fetch", async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url);
    if (path.includes(first.changeProposal.proposalRevisionId) && path.endsWith("/coverage")) {
      oldSignal = init?.signal as AbortSignal;
      return new Promise<Response>((resolve) => {
        finish = resolve;
      });
    }
    if (path.endsWith("/coverage"))
      return json(200, { ...envelope, pins, ...exactCoverage, source: exactSource() });
    if (path.includes("/artifacts/query")) {
      requests.push(JSON.parse(String(init?.body)));
      return json(200, {
        ...page,
        revisionId: pins.baseRevisionId,
        semanticHash: pins.effectiveSemanticHash,
        sourceSnapshotIds: pins.sourceSnapshotIds,
        target: second,
        effectivePins: pins,
        pins: [artifactPin],
        selectedPin: artifactPin,
        editorBindings: [],
      });
    }
    return json(500, { error: { message: "Unexpected source fallback" } });
  });
  const props = {
    projectId: exactIDs.project,
    readOnly: true,
    initialArtifact: "design_scenario:12",
    initialView: "sequence",
  };
  function Host() {
    const [target, setTarget] = useState(first);
    return (
      <>
        <button onClick={() => setTarget(second)}>Change owner target</button>
        <BackendArtifactProjections {...props} target={target} />
      </>
    );
  }
  renderWithProviders(<Host />);
  await waitFor(() => expect(oldSignal).toBeDefined());
  await userEvent.click(screen.getByRole("button", { name: "Change owner target" }));
  await waitFor(() => expect(oldSignal?.aborted).toBe(true));
  finish(
    json(200, {
      ...exactEnvelope(first),
      ...exactCoverage,
      pins: { ...exactEnvelope(first).pins, artifactPins: [artifactPin] },
      source: exactSource(),
    }),
  );
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0]).toMatchObject(second);
});
