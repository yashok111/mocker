import { afterEach, expect, it, vi } from "vitest";
import { cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { Inspector } from "./Inspector";
import { projectId, revisionId, workspaceHTTP } from "./testFixtures";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("closes a saved diagram selection with Escape but leaves an overlaid dialog in charge", async () => {
  workspaceHTTP();
  const close = vi.fn();
  renderWithProviders(
    <Inspector
      projectId={projectId}
      target={{ revisionId }}
      search={{}}
      id="collection:orders"
      node={{
        id: "collection:orders",
        kind: "collection",
        name: "Orders",
        description: "",
        attributes: {},
        parentId: null,
        childCount: 0,
      }}
      onClose={close}
      onEnter={vi.fn()}
      onNavigate={vi.fn()}
    />,
  );
  const dialog = document.createElement("div");
  dialog.setAttribute("role", "dialog");
  document.body.append(dialog);
  await userEvent.keyboard("{Escape}");
  expect(close).not.toHaveBeenCalled();
  dialog.remove();
  await userEvent.keyboard("{Escape}");
  expect(close).toHaveBeenCalledOnce();
});
