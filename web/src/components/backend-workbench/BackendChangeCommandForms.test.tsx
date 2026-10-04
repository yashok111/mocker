import { useState } from "react";
import { fireEvent, screen, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { BackendChangeCommandForms, changeCommandLabels } from "./BackendChangeCommandForms";
import { backendChangeSchemas } from "./backendChangeSchema";
import { parseChangeCommand } from "./backendChangeFormModel";
import { changeFixtureValue, changeTestID } from "./backendChangeTestFixtures";
import type { ChangeObject } from "./backendChangeSchemaTypes";
import type {
  BackendChangeProposalCommand,
  BackendEffectiveIdentity,
} from "@/api/generated/schemas";
afterEach(cleanup);

describe("full proposal typed command controls", () => {
  it.each(Object.keys(changeCommandLabels) as BackendChangeProposalCommand["type"][])(
    "edits and emits typed %s without a JSON patch field",
    (type) => {
      const schema = backendChangeSchemas.BackendChangeProposalCommand!.oneOf!.find(
        (branch) => branch.properties?.type?.const === type,
      )!;
      const command = parseChangeCommand(changeFixtureValue(schema) as ChangeObject);
      const onAdd = vi.fn();
      const identities: BackendEffectiveIdentity[] =
        command.type === "map_identity"
          ? [
              {
                target: command.target,
                externalKey: command.expectedExternalKey,
                origin: {
                  recordType: "node",
                  subjectId: changeTestID,
                  selector: { kind: "edge_name" },
                  kind: "source",
                  sourceClaims: [],
                  evidenceIds: [],
                },
              },
            ]
          : [];
      renderWithProviders(
        <BackendChangeCommandForms
          baseSchemaVersion="6"
          initial={command}
          identities={identities}
          onAdd={onAdd}
        />,
      );
      fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
        target: { value: "Reviewed change" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Сохранить локальную правку" }));
      expect(onAdd).toHaveBeenCalledWith({ ...command, reason: "Reviewed change" });
      expect(screen.queryByRole("textbox", { name: /JSON|commandId/i })).toBeNull();
    },
  );
  it("starts a rename through visible fields and retains a canonical hidden command ID", () => {
    const onAdd = vi.fn();
    renderWithProviders(<BackendChangeCommandForms baseSchemaVersion="6" onAdd={onAdd} />);
    fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
      target: { value: "Readable name" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "UUID объекта" }), {
      target: { value: changeTestID },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Название" }), {
      target: { value: "Desired name" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
    expect(onAdd).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "rename",
        id: changeTestID,
        name: "Desired name",
        recordType: "node",
        commandId: expect.stringMatching(/^[a-f0-9-]{36}$/),
      }),
    );
  });
  it("selects the full provider identity and exact intended key", () => {
    const identity: BackendEffectiveIdentity = {
      target: {
        kind: "source_identity",
        source: {
          recordType: "node",
          id: changeTestID,
          repositoryId: changeTestID,
          providerNamespace: "provider-b",
          externalKey: "source-key",
          assertionHash: "b".repeat(64),
        },
      },
      externalKey: "already-desired",
      origin: {
        recordType: "node",
        subjectId: changeTestID,
        selector: {
          kind: "source_identity",
          recordType: "node",
          id: changeTestID,
          repositoryId: changeTestID,
          providerNamespace: "provider-b",
        },
        kind: "base",
        sourceClaims: [],
        evidenceIds: [],
      },
    };
    const onAdd = vi.fn();
    renderWithProviders(
      <BackendChangeCommandForms baseSchemaVersion="6" identities={[identity]} onAdd={onAdd} />,
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
      target: { value: "map_identity" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Точная идентичность объекта" }), {
      target: { value: "0" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
      target: { value: "New intended alias" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Новый желаемый ключ" }), {
      target: { value: "next-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
    expect(onAdd).toHaveBeenCalledWith(
      expect.objectContaining({
        target: identity.target,
        expectedExternalKey: "already-desired",
        newExternalKey: "next-key",
      }),
    );
    expect(screen.getByRole("option", { name: /provider-b.*source-key/ })).toBeInTheDocument();
  });
  it("hides source6-only create kinds from a source5 proposal", () => {
    renderWithProviders(<BackendChangeCommandForms baseSchemaVersion="5" onAdd={vi.fn()} />);
    fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
      target: { value: "create_node" },
    });
    expect(screen.queryByRole("option", { name: "dto" })).toBeNull();
    expect(screen.queryByRole("option", { name: "representation_field" })).toBeNull();
  });
});

it("hides representation updates and value refs from source5 typed branches", () => {
  renderWithProviders(<BackendChangeCommandForms baseSchemaVersion="5" onAdd={vi.fn()} />);
  fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
    target: { value: "update_node" },
  });
  expect(screen.queryByRole("option", { name: /Вид: dto/ })).toBeNull();
  fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
    target: { value: "set_field_mapping" },
  });
  expect(screen.queryByRole("option", { name: /representation_field/ })).toBeNull();
});

it.each(
  backendChangeSchemas.BackendChangeCriterion!.oneOf!.map((schema, index) => ({ schema, index })),
)("renders complete typed criterion variant $index", ({ schema }) => {
  const criterion = changeFixtureValue(schema);
  const command = parseChangeCommand({
    type: "set_criteria",
    commandId: changeTestID,
    reason: "Explicit checks",
    criteria: [criterion],
  });
  const onAdd = vi.fn();
  renderWithProviders(
    <BackendChangeCommandForms baseSchemaVersion="6" initial={command} onAdd={onAdd} />,
  );
  expect(screen.getByRole("textbox", { name: "Ключ критерия" })).toBeInTheDocument();
  fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
    target: { value: "Reviewed checks" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Сохранить локальную правку" }));
  expect(onAdd).toHaveBeenCalledWith({ ...command, reason: "Reviewed checks" });
});

it("keeps explicit null for a first created identity assignment", () => {
  const identity: BackendEffectiveIdentity = {
    target: { kind: "intent_identity", recordType: "node", id: changeTestID },
    externalKey: null,
    origin: {
      recordType: "node",
      subjectId: changeTestID,
      selector: { kind: "intent_identity", recordType: "node", id: changeTestID },
      kind: "intent",
      sourceClaims: [],
      evidenceIds: [],
    },
  };
  const onAdd = vi.fn();
  renderWithProviders(
    <BackendChangeCommandForms baseSchemaVersion="6" identities={[identity]} onAdd={onAdd} />,
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
    target: { value: "map_identity" },
  });
  fireEvent.change(screen.getByRole("combobox", { name: "Точная идентичность объекта" }), {
    target: { value: "0" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
    target: { value: "First key" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Новый желаемый ключ" }), {
    target: { value: "new-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
  expect(onAdd).toHaveBeenCalledWith(
    expect.objectContaining({
      target: identity.target,
      expectedExternalKey: null,
      newExternalKey: "new-key",
    }),
  );
});

it("preserves the explicit object UUID when changing a pending create branch", () => {
  const schema = backendChangeSchemas.BackendChangeProposalCommand!.oneOf!.find(
    (branch) => branch.properties?.type?.const === "create_node",
  )!;
  const command = parseChangeCommand(changeFixtureValue(schema) as ChangeObject);
  renderWithProviders(
    <BackendChangeCommandForms baseSchemaVersion="6" initial={command} onAdd={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Вариант команды" }), {
    target: { value: "1" },
  });
  expect(screen.getByRole("textbox", { name: "UUID объекта" })).toHaveValue(changeTestID);
});

it("keeps a selected intent address when a baseline identity is inserted before it", () => {
  const intent: BackendEffectiveIdentity = {
    target: { kind: "intent_identity", recordType: "node", id: changeTestID },
    externalKey: null,
    origin: {
      recordType: "node",
      subjectId: changeTestID,
      selector: { kind: "intent_identity", recordType: "node", id: changeTestID },
      kind: "intent",
      sourceClaims: [],
      evidenceIds: [],
    },
  };
  const baseline: BackendEffectiveIdentity = {
    target: {
      kind: "source_identity",
      source: {
        recordType: "node",
        id: "0197aaf9-5555-7000-8000-000000000009",
        repositoryId: changeTestID,
        providerNamespace: "baseline",
        externalKey: "base-key",
        assertionHash: "c".repeat(64),
      },
    },
    externalKey: "base-key",
    origin: intent.origin,
  };
  const onAdd = vi.fn();
  function Host() {
    const [loaded, setLoaded] = useState(false);
    return (
      <>
        <button onClick={() => setLoaded(true)}>Загрузить базовые записи</button>
        <BackendChangeCommandForms
          baseSchemaVersion="6"
          identities={loaded ? [baseline, intent] : [intent]}
          onAdd={onAdd}
        />
      </>
    );
  }
  renderWithProviders(<Host />);
  fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
    target: { value: "map_identity" },
  });
  fireEvent.change(screen.getByRole("combobox", { name: "Точная идентичность объекта" }), {
    target: { value: "0" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Загрузить базовые записи" }));
  const selected = screen.getByRole("combobox", {
    name: "Точная идентичность объекта",
  }) as HTMLSelectElement;
  expect(selected.selectedOptions[0]?.textContent).toContain("созданный объект");
  fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
    target: { value: "Keep selected intent" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Новый желаемый ключ" }), {
    target: { value: "intent-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
  expect(onAdd).toHaveBeenCalledWith(
    expect.objectContaining({ target: intent.target, expectedExternalKey: null }),
  );
});

it.each(["reordered", "removed", "changed key"] as const)(
  "keeps two provider addresses distinct when the selected identity is %s",
  (update) => {
    function identity(provider: string): BackendEffectiveIdentity {
      return {
        target: {
          kind: "source_identity",
          source: {
            recordType: "node",
            id: changeTestID,
            repositoryId: changeTestID,
            providerNamespace: provider,
            externalKey: "same-source-key",
            assertionHash: "c".repeat(64),
          },
        },
        externalKey: `${provider}-desired`,
        origin: {
          recordType: "node",
          subjectId: changeTestID,
          selector: {
            kind: "source_identity",
            recordType: "node",
            id: changeTestID,
            repositoryId: changeTestID,
            providerNamespace: provider,
          },
          kind: "source",
          sourceClaims: [],
          evidenceIds: [],
        },
      };
    }
    const providerA = identity("provider-a");
    const providerB = identity("provider-b");
    const onAdd = vi.fn();
    function Host() {
      const [changed, setChanged] = useState(false);
      const next =
        update === "removed"
          ? [providerA]
          : update === "changed key"
            ? [providerA, { ...providerB, externalKey: "changed" }]
            : [providerB, providerA];
      return (
        <>
          <button onClick={() => setChanged(true)}>Обновить идентичности</button>
          <BackendChangeCommandForms
            baseSchemaVersion="6"
            identities={changed ? next : [providerA, providerB]}
            onAdd={onAdd}
          />
        </>
      );
    }
    renderWithProviders(<Host />);
    fireEvent.change(screen.getByRole("combobox", { name: "Команда изменения" }), {
      target: { value: "map_identity" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Точная идентичность объекта" }), {
      target: { value: "1" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Причина изменения" }), {
      target: { value: "Exact provider" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Новый желаемый ключ" }), {
      target: { value: "next" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Обновить идентичности" }));
    fireEvent.click(screen.getByRole("button", { name: "Добавить команду" }));
    if (update === "reordered")
      expect(onAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          target: providerB.target,
          expectedExternalKey: providerB.externalKey,
        }),
      );
    else {
      expect(onAdd).not.toHaveBeenCalled();
      expect(screen.getByRole("alert")).toHaveTextContent(
        update === "removed" ? "прежний адрес недоступен" : "Подтвердите выбор заново",
      );
    }
  },
);
