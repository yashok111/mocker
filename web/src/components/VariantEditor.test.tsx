import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fill } from "@/test/user";
import { useState } from "react";
import { VariantEditor, producerOf } from "./VariantEditor";
import { renderInRouter } from "@/test/render";
import { json, route } from "@/test/http";
import type { Variant } from "@/api/generated/schemas";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// Harness holds the variant the way both screens do: an updater that spreads.
function Harness({ initial, onChange }: { initial: Variant; onChange?: (v: Variant) => void }) {
  const [variant, setVariant] = useState<Variant>(initial);
  const [error, setError] = useState(false);
  return (
    <>
      <VariantEditor
        workspaceId={7}
        variant={variant}
        updateVariant={(u) =>
          setVariant((prev) => {
            const next = u(prev);
            onChange?.(next);
            return next;
          })
        }
        onErrorChange={setError}
        testId={(n) => `v-${n}`}
        whenTestId={(n, i) => `v-when-${n}-${i}`}
        hasSchema
        headersAppliedOn={["generated", "pinned", "file"]}
      />
      <output data-testid="v-error">{String(error)}</output>
    </>
  );
}

describe("producerOf", () => {
  it("reads the four producers off the variant, function first, then file, then mode", () => {
    expect(producerOf(undefined)).toBe("generated");
    expect(producerOf({ mode: "pinned", body: {} })).toBe("pinned");
    expect(producerOf({ mode: "pinned", bodyRef: "asset:a.png" })).toBe("file");
    expect(producerOf({ mode: "generated", function: "return 200" })).toBe("function");
  });
});

describe("VariantEditor", () => {
  it("switching the producer clears what the server refuses beside it, keeps when[] and headers", async () => {
    let last: Variant | undefined;
    route({
      "GET /api/workspaces/7/assets": () =>
        json(200, {
          assets: [
            {
              name: "logo.png",
              mediaType: "image/png",
              sizeBytes: 1,
              sha256: "x",
              createdAt: 0,
              updatedAt: 0,
              url: "",
            },
          ],
          totalBytes: 1,
          maxAssetBytes: 1,
          maxTotalBytes: 1,
        }),
    });
    renderInRouter(
      <Harness
        initial={{
          mode: "pinned",
          body: { ok: true },
          mediaType: "text/plain",
          headers: { "x-a": "1" },
          when: [{ in: "query", name: "q", op: "exists" }],
          recipes: { "$.id": { kind: "sequence" } },
        }}
        onChange={(v) => {
          last = v;
        }}
      />,
    );
    const mode = await screen.findByTestId("v-mode");
    expect(mode).toHaveValue("pinned");

    await userEvent.selectOptions(mode, "function");
    // The initial variant carries recipes: the switch asks before dropping them.
    expect(last).toBeUndefined();
    await userEvent.click(await screen.findByTestId("v-function-confirm"));
    expect(last).toMatchObject({ mode: "generated", function: "", headers: { "x-a": "1" } });
    // On the wire an undefined field is absent — compare the JSON form.
    const wire = () => JSON.parse(JSON.stringify(last)) as Record<string, unknown>;
    expect(wire()).not.toHaveProperty("body");
    expect(wire()).not.toHaveProperty("mediaType");
    expect(wire()).not.toHaveProperty("recipes");
    expect(last?.when).toHaveLength(1);
    // Chosen, not typed: the save is blocked.
    expect(screen.getByTestId("v-error")).toHaveTextContent("true");

    await userEvent.selectOptions(mode, "file");
    expect(last).toMatchObject({ mode: "pinned", bodyRef: "asset:" });
    expect(wire()).not.toHaveProperty("function");
    expect(screen.getByTestId("v-error")).toHaveTextContent("true");
    await userEvent.selectOptions(await screen.findByTestId("v-file"), "logo.png");
    expect(last?.bodyRef).toBe("asset:logo.png");
    expect(screen.getByTestId("v-error")).toHaveTextContent("false");

    await userEvent.selectOptions(mode, "pinned");
    expect(wire()).not.toHaveProperty("bodyRef");
    expect(screen.getByTestId("v-body")).toBeInTheDocument();
  });

  it("edits response headers as key/value rows", async () => {
    let last: Variant | undefined;
    renderInRouter(
      <Harness
        initial={{ mode: "generated" }}
        onChange={(v) => {
          last = v;
        }}
      />,
    );
    await userEvent.click(await screen.findByTestId("v-header-add"));
    await fill(screen.getByTestId("v-header-name-0"), "Location");
    await fill(screen.getByTestId("v-header-value-0"), "/users/1");
    expect(last?.headers).toEqual({ Location: "/users/1" });
    await userEvent.click(screen.getByTestId("v-header-remove-0"));
    expect(JSON.parse(JSON.stringify(last)) as object).not.toHaveProperty("headers");
  });

  it("blocks the save on a body that is not JSON and keeps the last valid one", async () => {
    let last: Variant | undefined;
    renderInRouter(
      <Harness
        initial={{ mode: "pinned", body: { ok: true } }}
        onChange={(v) => {
          last = v;
        }}
      />,
    );
    const body = await screen.findByTestId("v-body");
    await userEvent.clear(body);
    await fill(body, "{oops");
    expect(screen.getByTestId("v-error")).toHaveTextContent("true");
    expect(last).toBeUndefined();
    await userEvent.clear(body);
    await fill(body, '{"n": 2}');
    expect(screen.getByTestId("v-error")).toHaveTextContent("false");
    expect(last?.body).toEqual({ n: 2 });
  });

  it("shows agent-written schemaPatch as a count, never dropping it", async () => {
    renderInRouter(
      <Harness
        initial={{ mode: "generated", schemaPatch: [{ op: "add", path: "/x", value: 1 }] }}
      />,
    );
    expect(await screen.findByTestId("v-recipes")).toHaveTextContent(
      "Правок схемы на этом статусе: 1",
    );
  });

  it("seeds an empty body on the switch to a pinned body, so the wire carries what the box shows", async () => {
    let last: Variant | undefined;
    renderInRouter(
      <Harness
        initial={{ mode: "generated" }}
        onChange={(v) => {
          last = v;
        }}
      />,
    );
    await userEvent.selectOptions(await screen.findByTestId("v-mode"), "pinned");
    expect(last?.body).toEqual({});
  });

  it("keeps two header rows apart while one is being renamed, and never writes an unnamed one", async () => {
    let last: Variant | undefined;
    renderInRouter(
      <Harness
        initial={{ mode: "pinned", body: {}, headers: { "x-a": "1" } }}
        onChange={(v) => {
          last = v;
        }}
      />,
    );
    await userEvent.click(await screen.findByTestId("v-header-add"));
    await userEvent.click(screen.getByTestId("v-header-add"));
    expect(screen.getAllByTestId(/^v-header-name-/)).toHaveLength(3);
    await fill(screen.getByTestId("v-header-name-1"), "x-a");
    // Two rows named x-a on screen; the wire map has the later value.
    expect(screen.getAllByTestId(/^v-header-name-/)).toHaveLength(3);
    expect(last?.headers).toEqual({ "x-a": "" });
  });

  it("hides the headers list under a function and says the function sets them", async () => {
    renderInRouter(<Harness initial={{ mode: "generated", function: "return 200, {}" }} />);
    expect(await screen.findByTestId("v-headers-note")).toHaveTextContent("задаёт сама функция");
    expect(screen.queryByTestId("v-header-add")).not.toBeVisible();
  });
});
