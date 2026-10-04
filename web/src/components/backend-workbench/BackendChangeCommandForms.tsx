import { changeIdentityKey } from "./backendChangeState";
import { useState } from "react";
import { Alert, Button, Group, NativeSelect, Stack, Text } from "@mantine/core";
import type {
  BackendChangeProposalCommand,
  BackendChangeCriterion,
  BackendEffectiveIdentity,
} from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import { BackendChangeSchemaField, ChangeSchemaProfileContext } from "./BackendChangeSchemaField";
import {
  changeSchemaDefault,
  changeVariantIndex,
  isChangeObject,
  parseChangeCommand,
  resolveChangeSchema,
} from "./backendChangeFormModel";
import type { ChangeJSON, ChangeObject, ChangeSchema } from "./backendChangeSchemaTypes";

export const changeCommandLabels: Record<BackendChangeProposalCommand["type"], string> = {
  create_node: "Создать узел",
  update_node: "Изменить узел",
  rename: "Переименовать",
  remove_node: "Удалить узел",
  remove_edge: "Удалить связь",
  upsert_edge: "Создать или изменить связь",
  alter_column: "Изменить колонку",
  alter_constraint: "Изменить ограничение",
  alter_index: "Изменить индекс",
  edit_flow_step: "Изменить шаг",
  edit_branch: "Изменить переход",
  set_field_mapping: "Настроить отображение полей",
  set_artifact_pin: "Закрепить артефакт",
  remove_artifact_pin: "Снять артефакт",
  map_identity: "Изменить желаемый ключ",
  set_criteria: "Задать критерии",
};
const representationKinds = new Set(["domain_entity", "dto", "api_schema", "representation_field"]);
function commandBranches(
  type: BackendChangeProposalCommand["type"],
  baseSchemaVersion: string,
): ChangeSchema[] {
  return backendChangeSchemas.BackendChangeProposalCommand!.oneOf!.filter(
    (branch) =>
      branch.properties?.type?.const === type &&
      (baseSchemaVersion === "6" ||
        !representationKinds.has(String(branch.properties?.kind?.const))),
  );
}
function branchLabel(schema: ChangeSchema): string {
  const properties = schema.properties ?? {};
  return (
    [
      properties.kind?.const,
      properties.action?.const,
      properties.artifact && resolveChangeSchema(properties.artifact).properties?.kind?.const,
    ]
      .filter((value) => value !== undefined)
      .map(String)
      .join(" · ") || "Основной"
  );
}
function initialCommand(schema: ChangeSchema): ChangeObject {
  const value = changeSchemaDefault(schema);
  if (!isChangeObject(value)) throw new Error("Команда должна быть объектом");
  value.commandId = crypto.randomUUID();
  if (value.type === "create_node" || value.type === "upsert_edge") value.id = crypto.randomUUID();
  if (value.action === "create" && value.type === "alter_constraint")
    value.constraintId = crypto.randomUUID();
  if (value.action === "create" && value.type === "alter_index")
    value.indexId = crypto.randomUUID();
  if (value.type === "set_field_mapping") value.mappingId = crypto.randomUUID();
  return value;
}
export function BackendChangeCommandForms({
  baseSchemaVersion,
  initial,
  identities = [],
  onAdd,
  onCancel,
  onDirtyChange,
  criteria = [],
}: {
  baseSchemaVersion: "5" | "6";
  initial?: BackendChangeProposalCommand;
  identities?: BackendEffectiveIdentity[];
  onAdd: (command: BackendChangeProposalCommand) => void;
  onCancel?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
  criteria?: BackendChangeCriterion[];
}) {
  const [type, setType] = useState<BackendChangeProposalCommand["type"]>(initial?.type ?? "rename");
  const [value, setValue] = useState<ChangeObject>(() =>
    initial
      ? (structuredClone(initial) as unknown as ChangeObject)
      : initialCommand(commandBranches("rename", baseSchemaVersion)[0]!),
  );
  const [error, setError] = useState("");
  const [identityChoice, setIdentity] = useState<string | null>(null);
  const identityKey =
    identityChoice ?? (initial?.type === "map_identity" ? changeIdentityKey(initial.target) : "");
  const selectedIdentity = identities.find(
    (item) => changeIdentityKey(item.target) === identityKey,
  );
  const selectedIndex = selectedIdentity ? identities.indexOf(selectedIdentity) : -1;
  const identity = selectedIndex < 0 ? "" : String(selectedIndex);
  const branches = commandBranches(type, baseSchemaVersion);
  const branch = branches[changeVariantIndex(branches, value)]!;
  const isIdentity = type === "map_identity";
  function chooseType(next: BackendChangeProposalCommand["type"]) {
    onDirtyChange?.(true);
    setIdentity("");
    setType(next);
    const command = initialCommand(commandBranches(next, baseSchemaVersion)[0]!);
    if (next === "set_criteria")
      command.criteria = structuredClone(criteria) as unknown as ChangeJSON;
    setValue(command);
    setError("");
  }
  function chooseIdentity(index: string) {
    onDirtyChange?.(true);
    const picked = index === "" ? undefined : identities[Number(index)];
    setIdentity(picked ? changeIdentityKey(picked.target) : "");
    if (!picked) return;
    setValue((previous) => ({
      ...previous,
      target: structuredClone(picked.target) as unknown as ChangeJSON,
      expectedExternalKey: picked.externalKey,
    }));
  }
  const payload: ChangeSchema = {
    ...branch,
    properties: Object.fromEntries(
      Object.entries(branch.properties ?? {}).filter(
        ([key]) =>
          !["type", "commandId", ...(isIdentity ? ["target", "expectedExternalKey"] : [])].includes(
            key,
          ),
      ),
    ),
    required: branch.required?.filter(
      (key) =>
        !["type", "commandId", ...(isIdentity ? ["target", "expectedExternalKey"] : [])].includes(
          key,
        ),
    ),
  };
  return (
    <Stack gap="sm">
      <NativeSelect
        label="Команда изменения"
        value={type}
        data={Object.entries(changeCommandLabels).map(([key, label]) => ({ value: key, label }))}
        onChange={(event) =>
          chooseType(event.currentTarget.value as BackendChangeProposalCommand["type"])
        }
      />
      {branches.length > 1 && (
        <NativeSelect
          label="Вариант команды"
          value={String(branches.indexOf(branch))}
          data={branches.map((item, index) => ({ value: String(index), label: branchLabel(item) }))}
          onChange={(event) => {
            const selected = branches[Number(event.currentTarget.value)]!;
            const next = initialCommand(selected);
            for (const key of [
              "commandId",
              "reason",
              "id",
              "name",
              "parentId",
              "columnId",
              "constraintId",
              "indexId",
              "stepId",
              "edgeId",
              "mappingId",
              "tableId",
              "from",
              "to",
            ]) {
              if (Object.hasOwn(value, key) && Object.hasOwn(selected.properties ?? {}, key))
                next[key] = value[key]!;
            }
            onDirtyChange?.(true);
            setValue(next);
            setError("");
          }}
        />
      )}
      {baseSchemaVersion === "5" && (
        <Text size="xs" c="dimmed">
          Базовая схема 5: представления DTO и domain entity доступны после выбора source6 при
          создании предложения.
        </Text>
      )}
      {isIdentity && (
        <Stack gap="xs">
          <NativeSelect
            label="Точная идентичность объекта"
            value={identity}
            data={[
              { value: "", label: "Выберите qualified identity" },
              ...identities.map((item, index) => ({
                value: String(index),
                label:
                  item.target.kind === "source_identity"
                    ? `${item.target.source.recordType} ${item.target.source.id} · ${item.target.source.repositoryId} · ${item.target.source.providerNamespace} · ${item.target.source.externalKey} · ${item.target.source.assertionHash}`
                    : `${item.target.recordType} ${item.target.id} · созданный объект · ${item.externalKey ?? "ключ не задан"}`,
              })),
            ]}
            onChange={(event) => chooseIdentity(event.currentTarget.value)}
          />
          {selectedIdentity && (
            <>
              <Text size="sm">
                Текущий желаемый ключ: {selectedIdentity.externalKey ?? "null — первое присвоение"}
              </Text>
              {selectedIdentity.target.kind === "source_identity" && (
                <Text size="xs">Исходный ключ и assertion остаются в базовой ревизии.</Text>
              )}
            </>
          )}
          {identities.length === 0 && (
            <Alert color="yellow">
              Загрузите точные идентичности выбранной ревизии. Ключ нельзя выбирать по совпадению
              имени.
            </Alert>
          )}
        </Stack>
      )}
      <ChangeSchemaProfileContext.Provider value={baseSchemaVersion}>
        <BackendChangeSchemaField
          schema={payload}
          value={value}
          onChange={(next) => {
            onDirtyChange?.(true);
            if (isChangeObject(next)) setValue({ ...next, type, commandId: value.commandId! });
            setError("");
          }}
          label="Поля команды"
          hide={["type", "commandId"]}
        />
      </ChangeSchemaProfileContext.Provider>
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
      <Group>
        <Button
          onClick={() => {
            try {
              if (isIdentity && !selectedIdentity)
                throw new Error("Выберите точную идентичность: прежний адрес недоступен.");
              const command = parseChangeCommand(value, baseSchemaVersion);
              if (
                command.type === "map_identity" &&
                (changeIdentityKey(command.target) !== identityKey ||
                  command.expectedExternalKey !== selectedIdentity?.externalKey)
              )
                throw new Error(
                  "Идентичность или её текущий ключ изменились. Подтвердите выбор заново.",
                );
              onAdd(command);
              onDirtyChange?.(false);
              if (!initial) setValue(initialCommand(commandBranches(type, baseSchemaVersion)[0]!));
              setIdentity("");
              setError("");
            } catch (failure) {
              setError(failure instanceof Error ? failure.message : "Проверьте поля команды");
            }
          }}
        >
          {initial ? "Сохранить локальную правку" : "Добавить команду"}
        </Button>
        {onCancel && (
          <Button variant="subtle" onClick={onCancel}>
            Отменить редактирование
          </Button>
        )}
      </Group>
    </Stack>
  );
}
