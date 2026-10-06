import { MantineProvider } from "@mantine/core";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { BackendWorkspaceNavigation } from "./BackendWorkspaceNavigation";
it("opens a hidden exact section and moves keyboard focus without navigation", async () => {
  render(
    <MantineProvider>
      <BackendWorkspaceNavigation />
      <details>
        <summary>closed</summary>
        <section aria-label="База данных">
          <h2>Exact DB</h2>
        </section>
      </details>
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "База данных" }));
  await waitFor(() => expect(screen.getByText("Exact DB")).toHaveFocus());
  expect(screen.getByText("closed").parentElement).toHaveAttribute("open");
});
it("explains unavailable views without changing revision", () => {
  render(
    <MantineProvider>
      <BackendWorkspaceNavigation />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Потоки данных" }));
  expect(screen.getByRole("status")).toHaveTextContent("не подменяется текущей ревизией");
});
