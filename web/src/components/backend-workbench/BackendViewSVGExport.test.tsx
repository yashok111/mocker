import { expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { BackendViewSVGExport } from "./BackendViewSVGExport";
const pin = {
  projectId: "10000000-0000-4000-8000-000000000001",
  viewId: "10000000-0000-4000-8000-000000000003",
  viewVersion: 2,
};
it("exports the exact saved pin with keyboard and shows filename and scope", async () => {
  const user = userEvent.setup();
  const run = vi.fn(async () => {});
  renderWithProviders(
    <BackendViewSVGExport pin={pin} name="Orders" scope="Context" exportFile={run} />,
  );
  const button = screen.getByRole("button", { name: "Скачать SVG" });
  button.focus();
  await user.keyboard("{Enter}");
  await waitFor(() => expect(run).toHaveBeenCalledWith(pin));
  expect(screen.getByText(/backend-view-.*-v2.svg/)).toBeInTheDocument();
  expect(screen.getByText(/Context/)).toBeInTheDocument();
});
it("rejects unsafe versions", () => {
  renderWithProviders(
    <BackendViewSVGExport
      pin={{ ...pin, viewVersion: Number.MAX_SAFE_INTEGER + 1 }}
      name="Orders"
      scope="Context"
      exportFile={vi.fn()}
    />,
  );
  expect(screen.getByRole("button", { name: "Скачать SVG" })).toBeDisabled();
});
it("announces export failures", async () => {
  const user = userEvent.setup();
  const run = vi.fn(async () => {
    throw new Error("Слишком большой вид");
  });
  renderWithProviders(
    <BackendViewSVGExport pin={pin} name="Orders" scope="Context" exportFile={run} />,
  );
  await user.click(screen.getByRole("button", { name: "Скачать SVG" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Слишком большой вид");
});
