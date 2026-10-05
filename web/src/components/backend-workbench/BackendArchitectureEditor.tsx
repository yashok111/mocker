import {
  Button,
  Group,
  NativeSelect,
  Paper,
  Stack,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { useState } from "react";
import type {
  BackendArchitectureDocument,
  BackendArchitectureElement,
} from "@/api/generated/schemas";
export function BackendArchitectureEditor({
  document,
  onChange,
  onSave,
  onCancel,
  busy,
}: {
  document: BackendArchitectureDocument;
  onChange: (d: BackendArchitectureDocument) => void;
  onSave: () => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [label, setLabel] = useState("");
  const [role, setRole] = useState<BackendArchitectureElement["role"]>("application");
  const [parent, setParent] = useState(document.payload.primarySystemId);
  const [reason, setReason] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [relation, setRelation] = useState("calls");
  const [raw, setRaw] = useState<string | null>(null);
  const [error, setError] = useState("");
  const update = (next: BackendArchitectureDocument) => {
    if (error) return;
    setRaw(null);
    onChange(next);
  };
  const nodes = document.payload.elements.map((e) => ({
    value: e.id,
    label: `${e.label} · ${e.role}`,
  }));
  const primary = document.payload.elements.find((e) => e.id === document.payload.primarySystemId);
  const add = () => {
    if (!label.trim() || !reason.trim()) return;
    update({
      ...document,
      payload: {
        ...document.payload,
        elements: [
          ...document.payload.elements,
          {
            id: crypto.randomUUID(),
            label,
            role,
            ...(["person", "software_system"].includes(role) ? {} : { parentId: parent }),
            responsibility: "",
            technology: "",
            origin: { kind: "authored", reason },
            refs: [],
          },
        ],
      },
    });
    setLabel("");
  };
  return (
    <Paper withBorder p="md">
      <Stack aria-label="Редактор mapping">
        <Title order={3}>Явный архитектурный mapping</Title>
        <Text size="sm">
          Границы и связи сохраняются только по вашему подтверждению. Исходная модель остаётся
          неизменной.
        </Text>
        <TextInput
          label="Название системы"
          value={primary?.label ?? ""}
          disabled={busy || !!error || !!error}
          onChange={(e) =>
            update({
              ...document,
              payload: {
                ...document.payload,
                elements: document.payload.elements.map((n) =>
                  n.id === primary?.id ? { ...n, label: e.currentTarget.value } : n,
                ),
              },
            })
          }
        />
        <Group grow align="end">
          <TextInput
            label="Название элемента"
            value={label}
            onChange={(e) => setLabel(e.currentTarget.value)}
            disabled={busy || !!error || !!error}
          />
          <NativeSelect
            label="Роль C4"
            value={role}
            onChange={(e) => setRole(e.currentTarget.value as BackendArchitectureElement["role"])}
            data={["person", "software_system", "application", "data_store", "component"]}
            disabled={busy || !!error || !!error}
          />
          <NativeSelect
            label="Родитель"
            value={parent}
            onChange={(e) => setParent(e.currentTarget.value)}
            data={nodes}
            disabled={busy || !!error || !!error}
          />
        </Group>
        <TextInput
          label="Основание авторского решения"
          value={reason}
          onChange={(e) => setReason(e.currentTarget.value)}
          disabled={busy || !!error || !!error}
        />
        <Button onClick={add} disabled={busy || !!error || !label.trim() || !reason.trim()}>
          Добавить элемент
        </Button>
        <Group grow align="end">
          <NativeSelect
            label="Связь от"
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value)}
            data={[{ value: "", label: "Выберите элемент" }, ...nodes]}
            disabled={busy || !!error || !!error}
          />
          <NativeSelect
            label="Связь к"
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
            data={[{ value: "", label: "Выберите элемент" }, ...nodes]}
            disabled={busy || !!error || !!error}
          />
          <TextInput
            label="Отношение"
            value={relation}
            onChange={(e) => setRelation(e.currentTarget.value)}
            disabled={busy || !!error || !!error}
          />
        </Group>
        <Button
          disabled={busy || !!error || !from || !to || !reason.trim() || !relation.trim()}
          onClick={() =>
            update({
              ...document,
              payload: {
                ...document.payload,
                links: [
                  ...document.payload.links,
                  {
                    id: crypto.randomUUID(),
                    label: relation,
                    from,
                    to,
                    relation,
                    origin: { kind: "authored", reason },
                    refs: [],
                  },
                ],
              },
            })
          }
        >
          Добавить авторскую связь
        </Button>
        <Textarea
          label="Элементы, source refs и evidence (JSON)"
          description="Точные ID, responsibility, technology и origin. Сервер проверяет принадлежность source refs и evidence."
          value={raw ?? JSON.stringify(document.payload, null, 2)}
          minRows={8}
          maxRows={18}
          autosize
          disabled={busy}
          onChange={(e) => {
            const text = e.currentTarget.value;
            setRaw(text);
            try {
              const payload = JSON.parse(text);
              if (
                !payload ||
                typeof payload.primarySystemId !== "string" ||
                !Array.isArray(payload.elements) ||
                !Array.isArray(payload.links) ||
                !payload.elements.every(
                  (e: unknown) =>
                    !!e &&
                    typeof e === "object" &&
                    "id" in e &&
                    typeof e.id === "string" &&
                    "label" in e &&
                    typeof e.label === "string" &&
                    "role" in e &&
                    typeof e.role === "string" &&
                    "refs" in e &&
                    Array.isArray(e.refs),
                )
              )
                throw new Error("Invalid architecture payload");
              onChange({ ...document, payload });
              setError("");
            } catch {
              setError("Исправьте JSON перед сохранением");
            }
          }}
          error={error}
        />
        <Group>
          <Button disabled={busy || !!error || !primary?.label.trim()} onClick={onSave}>
            Сохранить mapping
          </Button>
          <Button variant="default" disabled={busy} onClick={onCancel}>
            Отменить редактирование
          </Button>
        </Group>
      </Stack>
    </Paper>
  );
}
