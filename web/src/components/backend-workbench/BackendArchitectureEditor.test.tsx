import { useState } from "react";
import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/render";
import { fill } from "@/test/user";
import { BackendArchitectureEditor } from "./BackendArchitectureEditor";
import type { BackendArchitectureDocument } from "@/api/generated/schemas";
it("keeps structured edits and valid advanced JSON on the same draft", async () => {
  const user = userEvent.setup();
  const id = "10000000-0000-4000-8000-000000000001";
  const initial: BackendArchitectureDocument = {
    format: "backend-diagram-v1",
    kind: "architecture",
    target: { revisionId: id },
    payload: {
      primarySystemId: id,
      elements: [
        {
          id,
          label: "Orders",
          role: "software_system",
          responsibility: "",
          technology: "",
          origin: { kind: "authored", reason: "Explicit" },
          refs: [],
        },
      ],
      links: [],
    },
  };
  function Harness() {
    const [d, setD] = useState(initial);
    return (
      <BackendArchitectureEditor
        document={d}
        onChange={setD}
        onSave={vi.fn()}
        onCancel={vi.fn()}
        busy={false}
      />
    );
  }
  renderWithProviders(<Harness />);
  const advanced = screen.getByLabelText("Элементы, source refs и evidence (JSON)");
  const payload = structuredClone(initial.payload);
  payload.elements[0]!.responsibility = "Preserve this explicit responsibility";
  await user.clear(advanced);
  await fill(advanced, JSON.stringify(payload));
  await user.clear(screen.getByLabelText("Название системы"));
  await user.type(screen.getByLabelText("Название системы"), "Renamed");
  const shown = JSON.parse((advanced as HTMLTextAreaElement).value);
  expect(shown.elements[0].label).toBe("Renamed");
  expect(shown.elements[0].responsibility).toBe(payload.elements[0]!.responsibility);
});
