import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { json } from "@/test/http";
import type { SchemaModel } from "@/api/generated/schemas";
import { createFormDraftStore } from "../api-designer/forms/formDraftStore";
import SchemaModelEditor from "./SchemaModelEditor";

vi.mock("../diagram/elkLayout", () => ({
  layoutDiagram: vi.fn(
    async (input: { nodes: { id: string; width: number; height: number }[] }) => ({
      nodes: input.nodes.map((node, index) => ({ ...node, x: 480, y: 40 + index * 200 })),
      edges: [],
    }),
  ),
}));
vi.mock("./SchemaGraph", () => ({
  default: ({
    model,
    disabled,
    onMove,
    fitIdentity,
  }: {
    model: SchemaModel;
    disabled: boolean;
    onMove: (id: string, x: number, y: number) => void;
    fitIdentity?: number;
  }) => (
    <>
      <output data-testid="graph" data-disabled={disabled} data-fit-identity={fitIdentity}>
        {model.schemas[0]?.x}
      </output>
      <button disabled={disabled} onClick={() => onMove("Order0", 700, 500)}>
        Переместить карточку
      </button>
    </>
  ),
}));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const document = '{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{}}';
const model = (count = 2): SchemaModel => ({
  schemas: Array.from({ length: count }, (_, index) => ({
    name: `Order${index}`,
    pointer: "",
    schemaJSON: "{}",
    type: "object",
    description: "",
    properties: [],
    x: 40,
    y: 40,
  })),
  references: [],
  operations: [],
});
function requests(
  count = 2,
  commandResponse?: (body: {
    document: string;
    commands: { kind: string; schemaName: string; x: number; y: number }[];
  }) => Response | Promise<Response>,
) {
  const fetchMock = vi.fn((_url: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body));
    if (body.commands && commandResponse) return Promise.resolve(commandResponse(body));
    const current = model(count);
    for (const command of body.commands ?? []) {
      const schema = current.schemas.find((schema) => schema.name === command.schemaName);
      if (schema) Object.assign(schema, { x: command.x, y: command.y });
    }
    return Promise.resolve(
      json(200, {
        document: body.document + (body.commands ? " " : ""),
        model: current,
        valid: true,
        diagnostics: [],
      }),
    );
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}
function Harness({ onChange = () => {} }: { onChange?: (document: string) => void }) {
  const [buffer, setBuffer] = useState(document);
  const [pending, setPending] = useState(false);
  const [mounted, setMounted] = useState(true);
  const [store] = useState(createFormDraftStore);
  return (
    <>
      {mounted && (
        <SchemaModelEditor
          designId={12}
          document={buffer}
          blocked={false}
          formStore={store}
          onChange={(next) => {
            onChange(next);
            setBuffer(next);
          }}
          onLayoutPendingChange={setPending}
        />
      )}
      <output data-testid="buffer">{buffer}</output>
      <button disabled={pending}>Сохранить</button>
      <button onClick={() => setBuffer(document + "\n")}>Изменить документ</button>
      <button onClick={() => setMounted(false)}>Закрыть редактор</button>
    </>
  );
}

describe("schema layout proposal", () => {
  it("requests a fresh fit only when the proposal is displayed and cancelled", async () => {
    requests();
    renderWithProviders(<Harness />);
    await screen.findByTestId("graph");
    expect(screen.getByTestId("graph")).toHaveAttribute("data-fit-identity", "0");
    await userEvent.click(screen.getByRole("button", { name: "Order0" }));
    expect(screen.getByTestId("graph")).toHaveAttribute("data-fit-identity", "0");
    await userEvent.click(screen.getByRole("button", { name: "Расставить схемы" }));
    await screen.findByRole("button", { name: "Применить расположение" });
    expect(screen.getByTestId("graph")).toHaveAttribute("data-fit-identity", "1");
    await userEvent.click(screen.getByRole("button", { name: "Отменить" }));
    expect(screen.getByTestId("graph")).toHaveAttribute("data-fit-identity", "2");
    expect(screen.getByTestId("buffer").textContent).toBe(document);
  });
  it("previews all positions and applies them as one draft update", async () => {
    const fetchMock = requests();
    const onChange = vi.fn();
    renderWithProviders(<Harness onChange={onChange} />);
    await userEvent.click(await screen.findByRole("button", { name: "Расставить схемы" }));
    await screen.findByRole("button", { name: "Применить расположение" });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId("graph")).toHaveTextContent("480");
    expect(screen.getByTestId("graph")).toHaveAttribute("data-disabled", "true");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeDisabled();
    const body = fetchMock.mock.calls
      .map(([, init]) => JSON.parse(String(init?.body)))
      .find((body) => body.commands);
    expect(body.commands).toEqual([
      { kind: "move_schema", schemaName: "Order0", x: 480, y: 40 },
      { kind: "move_schema", schemaName: "Order1", x: 480, y: 240 },
    ]);
    await userEvent.click(screen.getByRole("button", { name: "Применить расположение" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(document + " ");
    await userEvent.click(screen.getByRole("button", { name: "Переместить карточку" }));
    await waitFor(() => expect(onChange).toHaveBeenCalledTimes(2));
    expect(
      fetchMock.mock.calls
        .map(([, init]) => JSON.parse(String(init?.body)))
        .filter((body) => body.commands)
        .at(-1).commands,
    ).toEqual([{ kind: "move_schema", schemaName: "Order0", x: 700, y: 500 }]);
  });

  it("chains 101 schemas through two previews and never applies a partial batch", async () => {
    const bodies: { document: string; commands: unknown[] }[] = [];
    requests(101, (body) => {
      bodies.push(body);
      return json(200, {
        document: body.document + " ",
        model: model(101),
        valid: true,
        diagnostics: [],
      });
    });
    const onChange = vi.fn();
    renderWithProviders(<Harness onChange={onChange} />);
    await userEvent.click(await screen.findByRole("button", { name: "Расставить схемы" }));
    await screen.findByRole("button", { name: "Применить расположение" });
    expect(bodies.map((body) => [body.document, body.commands.length])).toEqual([
      [document, 100],
      [document + " ", 1],
    ]);
    expect(onChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Применить расположение" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(document + "  ");
  });

  it.each(["Отменить", "Изменить документ", "Закрыть редактор"])(
    "discards a late proposal after %s",
    async (action) => {
      let finish!: (response: Response) => void;
      requests(
        2,
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );
      const onChange = vi.fn();
      renderWithProviders(<Harness onChange={onChange} />);
      await userEvent.click(await screen.findByRole("button", { name: "Расставить схемы" }));
      await waitFor(() => expect(finish).toBeDefined());
      await userEvent.click(screen.getByRole("button", { name: action }));
      await act(async () =>
        finish(json(200, { document: "stale", model: model(), valid: true, diagnostics: [] })),
      );
      expect(onChange).not.toHaveBeenCalled();
      expect(
        screen.queryByRole("button", { name: "Применить расположение" }),
      ).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
    },
  );
});
