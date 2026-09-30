import { afterEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { route } from "@/test/http";
import { MockTester } from "./MockTester";

afterEach(() => vi.unstubAllGlobals());

it("displays committed HTTP entity JSON without changing numeric tokens", async () => {
  const body =
    '{"status":"paid","large":9007199254740993,"fraction":1.0000000000000000001,"exponent":1e+09,"negativeZero":-0}';
  route({
    "POST http://draft.mock.local/orders/7/pay": () =>
      new Response(body, { status: 200, headers: { "Content-Type": "application/json" } }),
  });
  renderWithProviders(
    <MockTester
      draftUrl="http://draft.mock.local"
      publishedUrl="http://published.mock.local"
      published={false}
      initialMethod="POST"
      initialPath="/orders/7/pay"
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Вызвать мок" }));
  await screen.findByText("Ответ · HTTP 200");
  expect(screen.getByText(/negativeZero/).textContent).toBe(body);
});
