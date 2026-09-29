import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json, route } from "@/test/http";
import SimulationPanel from "./SimulationPanel";
import { headerTemplate } from "./model";
const rule = { ...headerTemplate(), id: "auth" };
const source = { designId: 12, ruleId: "auth", kind: "proposal", documentHash: "sha256:exact" };
const result = {
  valid: true,
  diagnostics: [],
  diagnosticsTruncated: false,
  source,
  inputHash: "sha256:input",
  outcome: "response",
  trace: [{ step: 1, nodeId: "ok" }],
  totalDelayMs: 250,
  response: {
    status: 200,
    mediaType: "application/json",
    headers: [],
    bodyJSON: '{"n":9007199254740993}',
  },
};
const props = {
  designId: 12,
  document: '{"exact":9007199254740993}\n',
  rule,
  generation: "{}",
  pendingForm: false,
  onSelect: vi.fn(),
  onTrace: vi.fn(),
};
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
describe("response rule simulation", () => {
  it("sends the exact source and ordered fixture, and renders raw JSON as escaped text", async () => {
    const fetch = route({
      "POST /api/designs/12/response-rules/auth/simulate": () => json(200, result),
    });
    renderWithProviders(<SimulationPanel {...props} />);
    await userEvent.click(screen.getByRole("button", { name: "Добавить query-параметр" }));
    await userEvent.click(screen.getByRole("button", { name: "Добавить query-параметр" }));
    await userEvent.click(screen.getByLabelText("Тело запроса JSON"));
    expect(screen.getByLabelText("JSON запроса")).toHaveValue("null");
    await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
    expect(await screen.findByText('{"n":9007199254740993}')).toBeInTheDocument();
    expect(screen.getByText("Суммарная задержка: 250 мс")).toBeInTheDocument();
    const sent = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body));
    expect(sent.document).toBe(props.document);
    expect(sent.request).toEqual({
      query: [
        { name: "", value: "" },
        { name: "", value: "" },
      ],
      headers: [],
      bodyJSON: "null",
    });
  });
  it("discards a late result after pending inspector changes and a newer simulation", async () => {
    const replies: ((response: Response) => void)[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>((resolve) => replies.push(resolve))),
    );
    const rendered = render(
      <MantineProvider>
        <SimulationPanel {...props} />
      </MantineProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
    rendered.rerender(
      <MantineProvider>
        <SimulationPanel {...props} generation="pending" pendingForm />
      </MantineProvider>,
    );
    expect(screen.getByRole("button", { name: "Симулировать" })).toBeDisabled();
    rendered.rerender(
      <MantineProvider>
        <SimulationPanel {...props} generation="applied" />
      </MantineProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
    await waitFor(() => expect(replies).toHaveLength(2));
    await act(async () =>
      replies[1]!(json(200, { ...result, outcome: "fallback", response: undefined })),
    );
    expect(await screen.findByText(/Передать стандартной обработке/)).toBeInTheDocument();
    await act(async () => replies[0]!(json(200, result)));
    expect(screen.queryByText('{"n":9007199254740993}')).not.toBeInTheDocument();
  });
  it.each(["document", "rule", "fixture", "unmount"])(
    "rejects pending results after changing %s",
    async (change) => {
      let finish: ((response: Response) => void) | undefined;
      vi.stubGlobal(
        "fetch",
        vi.fn(
          () =>
            new Promise<Response>((resolve) => {
              finish = resolve;
            }),
        ),
      );
      const rendered = render(
        <MantineProvider>
          <SimulationPanel {...props} />
        </MantineProvider>,
      );
      await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
      if (change === "fixture")
        await userEvent.click(screen.getByRole("button", { name: "Добавить заголовок запроса" }));
      if (change === "document")
        rendered.rerender(
          <MantineProvider>
            <SimulationPanel {...props} document={`${props.document} `} />
          </MantineProvider>,
        );
      if (change === "rule")
        rendered.rerender(
          <MantineProvider>
            <SimulationPanel {...props} rule={{ ...rule, id: "other" }} />
          </MantineProvider>,
        );
      if (change === "unmount") rendered.unmount();
      await act(async () => finish!(json(200, result)));
      expect(screen.queryByText('{"n":9007199254740993}')).not.toBeInTheDocument();
    },
  );
  it("does not resurrect a completed trace after an edit is reverted", async () => {
    route({ "POST /api/designs/12/response-rules/auth/simulate": () => json(200, result) });
    const rendered = render(
      <MantineProvider>
        <SimulationPanel {...props} />
      </MantineProvider>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
    await screen.findByText('{"n":9007199254740993}');
    rendered.rerender(
      <MantineProvider>
        <SimulationPanel {...props} document="changed" />
      </MantineProvider>,
    );
    rendered.rerender(
      <MantineProvider>
        <SimulationPanel {...props} />
      </MantineProvider>,
    );
    expect(screen.queryByText('{"n":9007199254740993}')).not.toBeInTheDocument();
    expect(screen.getByText(/Результат устарел/)).toBeInTheDocument();
  });
  it("navigates semantic diagnostics and omits absent request bodies", async () => {
    const onSelect = vi.fn(),
      onSource = vi.fn();
    const fetch = route({
      "POST /api/designs/12/response-rules/auth/simulate": () =>
        json(200, {
          ...result,
          valid: false,
          outcome: "invalid",
          trace: [],
          response: undefined,
          totalDelayMs: undefined,
          diagnostics: [
            {
              severity: "error",
              code: "missing_exit",
              message: "Нет выхода Да",
              pointer: "/x-mocker-response-rules/rules/0/nodes/1",
              nodeId: "auth",
            },
          ],
        }),
    });
    renderWithProviders(<SimulationPanel {...props} onSelect={onSelect} onSource={onSource} />);
    await userEvent.click(screen.getByLabelText("Тело запроса JSON"));
    await userEvent.click(screen.getByLabelText("Тело запроса JSON"));
    await userEvent.click(screen.getByRole("button", { name: "Симулировать" }));
    await userEvent.click(await screen.findByRole("button", { name: /Нет выхода Да/ }));
    expect(onSelect).toHaveBeenCalledWith({ kind: "node", id: "auth" });
    await userEvent.click(screen.getByRole("button", { name: "Поле в исходнике" }));
    expect(onSource).toHaveBeenCalledWith("/x-mocker-response-rules/rules/0/nodes/1");
    expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)).request).not.toHaveProperty(
      "bodyJSON",
    );
    expect(screen.queryByText(/Суммарная задержка/)).not.toBeInTheDocument();
  });
});
