import { useState, type ReactElement } from "react";
import { Alert, Button, Group, NativeSelect, Stack, Text, Textarea } from "@mantine/core";
import {
  mergeFragmentBranch,
  patchCanvasFragment,
  setFragmentKind,
  splitFragmentBranch,
  upgradeFragmentDocument,
} from "./canvasFragments";
import type { CanvasDocument, CanvasFragment } from "./types";

export function CanvasFragmentInspector({
  document,
  fragment,
  onChange,
}: {
  document: CanvasDocument;
  fragment: CanvasFragment;
  onChange: (document: CanvasDocument) => void;
}): ReactElement {
  const [error, setError] = useState("");
  const [pending, setPending] = useState<Partial<CanvasFragment> | null>(null);
  const edited = pending ? { ...fragment, ...pending } : fragment;
  const positions = new Map(document.messages.map((m, i) => [m.id, i]));
  const first = Math.min(
    positions.get(edited.fromMessageId) ?? 0,
    positions.get(edited.toMessageId) ?? 0,
  );
  const last = Math.max(
    positions.get(edited.fromMessageId) ?? 0,
    positions.get(edited.toMessageId) ?? 0,
  );
  const run = (action: () => CanvasDocument) => {
    try {
      const result = action();
      onChange(result);
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Не удалось изменить блок");
    }
  };
  const patch = (changes: Partial<CanvasFragment>) =>
    run(() => {
      if (
        document.formatVersion === 1 &&
        !Object.hasOwn(changes, "parentFragmentId") &&
        !Object.hasOwn(changes, "parentBranchId")
      ) {
        // Keep legacy ranges repairable even when overlapping frames prevent migration.
        return {
          ...document,
          fragments: document.fragments.map((f) =>
            f.id === fragment.id
              ? {
                  ...f,
                  fromMessageId: document.messages[first]!.id,
                  toMessageId: document.messages[last]!.id,
                  ...changes,
                }
              : f,
          ),
        };
      }
      return patchCanvasFragment(document, fragment.id, changes);
    });
  const patchBounds = (from: string, to: string) => {
    if (pending) {
      setPending({ ...pending, fromMessageId: from, toMessageId: to });
      return;
    }
    patch({
      fromMessageId: from,
      toMessageId: to,
      ...(fragment.branches
        ? {
            branches: fragment.branches.map((b, i, all) => ({
              ...b,
              ...(i === 0 ? { fromMessageId: from } : {}),
              ...(i === all.length - 1 ? { toMessageId: to } : {}),
            })),
          }
        : {}),
    });
  };
  const descendants = new Set([fragment.id]);
  for (let i = 0; i < document.fragments.length; i++) {
    for (const f of document.fragments)
      if (f.parentFragmentId && descendants.has(f.parentFragmentId)) descendants.add(f.id);
  }
  const parents = document.fragments.filter((f) => !descendants.has(f.id));
  const parent = document.fragments.find((f) => f.id === edited.parentFragmentId);
  const steps = document.messages.map((m, i) => ({ value: m.id, label: `${i + 1}. ${m.label}` }));
  return (
    <>
      {error ? (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      ) : null}
      <NativeSelect
        label="Тип блока"
        value={fragment.kind}
        data={[
          { value: "opt", label: "Условие (opt)" },
          { value: "loop", label: "Цикл (loop)" },
          { value: "alt", label: "Ветвление (alt/else)" },
        ]}
        onChange={(e) => {
          const kind = e.currentTarget.value as CanvasFragment["kind"];
          run(() => setFragmentKind(document, fragment.id, kind));
        }}
      />
      <Textarea
        label={fragment.kind === "alt" ? "Название блока" : "Условие блока"}
        autosize
        minRows={2}
        value={fragment.label}
        onChange={(e) => patch({ label: e.currentTarget.value })}
      />
      <NativeSelect
        label="Внутри блока"
        value={edited.parentFragmentId ?? ""}
        data={[
          { value: "", label: "Верхний уровень" },
          ...parents.map((f) => ({ value: f.id, label: `${f.kind}: ${f.label}` })),
        ]}
        onChange={(e) => {
          const id = e.currentTarget.value;
          const target = document.fragments.find((f) => f.id === id);
          const branch =
            target?.branches?.find(
              (b) =>
                positions.get(b.fromMessageId)! <= first && positions.get(b.toMessageId)! >= last,
            ) ?? target?.branches?.[0];
          setPending({
            parentFragmentId: id || undefined,
            parentBranchId: branch?.id,
            fromMessageId: document.messages[first]!.id,
            toMessageId: document.messages[last]!.id,
          });
          setError("");
        }}
      />
      {parent?.kind === "alt" ? (
        <NativeSelect
          label="Ветка родительского блока"
          value={edited.parentBranchId ?? ""}
          data={(parent.branches ?? []).map((b, i) => ({
            value: b.id,
            label: `${i + 1}. ${b.label}`,
          }))}
          onChange={(e) => {
            setPending({
              ...pending,
              parentFragmentId: parent.id,
              parentBranchId: e.currentTarget.value,
              fromMessageId: edited.fromMessageId,
              toMessageId: edited.toMessageId,
            });
            setError("");
          }}
        />
      ) : null}
      <NativeSelect
        label="Первый шаг блока"
        value={document.messages[first]?.id ?? ""}
        data={steps}
        onChange={(e) => {
          const id = e.currentTarget.value;
          patchBounds(id, document.messages[Math.max(positions.get(id)!, last)]!.id);
        }}
      />
      <NativeSelect
        label="Последний шаг блока"
        value={document.messages[last]?.id ?? ""}
        data={steps.slice(first)}
        onChange={(e) => patchBounds(document.messages[first]!.id, e.currentTarget.value)}
      />
      {pending ? (
        <>
          <Text size="sm" c="dimmed">
            Выберите границы блока внутри новой области и примените изменения.
          </Text>
          <Group>
            <Button
              size="xs"
              onClick={() =>
                run(() => {
                  const changes = {
                    ...pending,
                    ...(fragment.branches
                      ? {
                          branches: fragment.branches.map((b, i, all) => ({
                            ...b,
                            ...(i === 0 ? { fromMessageId: edited.fromMessageId } : {}),
                            ...(i === all.length - 1 ? { toMessageId: edited.toMessageId } : {}),
                          })),
                        }
                      : {}),
                  };
                  const result = patchCanvasFragment(
                    upgradeFragmentDocument(document),
                    fragment.id,
                    changes,
                  );
                  setPending(null);
                  return result;
                })
              }
            >
              Применить вложенность
            </Button>
            <Button
              size="xs"
              variant="default"
              onClick={() => {
                setPending(null);
                setError("");
              }}
            >
              Отменить изменение вложенности
            </Button>
          </Group>
        </>
      ) : null}
      {fragment.kind === "alt" ? (
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            Ветки выполняются по одному из условий. Для последней ветки можно указать «else».
          </Text>
          {fragment.branches?.map((branch, index, branches) => (
            <Stack key={branch.id} gap="xs">
              <Textarea
                label={`Условие ветки ${index + 1}`}
                autosize
                minRows={2}
                value={branch.label}
                onChange={(e) =>
                  patch({
                    branches: branches.map((b) =>
                      b.id === branch.id ? { ...b, label: e.currentTarget.value } : b,
                    ),
                  })
                }
              />
              <Text size="xs" c="dimmed">
                Шаги {positions.get(branch.fromMessageId)! + 1}–
                {positions.get(branch.toMessageId)! + 1}
              </Text>
              {index < branches.length - 1 ? (
                <NativeSelect
                  label={`Последний шаг ветки ${index + 1}`}
                  value={branch.toMessageId}
                  data={steps.slice(
                    positions.get(branch.fromMessageId)!,
                    positions.get(branches[index + 1]!.toMessageId)!,
                  )}
                  onChange={(e) => {
                    const end = e.currentTarget.value;
                    const next = document.messages[positions.get(end)! + 1]!.id;
                    patch({
                      branches: branches.map((b, i) =>
                        i === index
                          ? { ...b, toMessageId: end }
                          : i === index + 1
                            ? { ...b, fromMessageId: next }
                            : b,
                      ),
                    });
                  }}
                />
              ) : null}
              <Group gap="xs">
                <Button
                  size="xs"
                  variant="default"
                  disabled={branch.fromMessageId === branch.toMessageId || branches.length >= 100}
                  onClick={() => run(() => splitFragmentBranch(document, fragment.id, branch.id))}
                >
                  Разделить ветку {index + 1}
                </Button>
                <Button
                  size="xs"
                  variant="subtle"
                  color="red"
                  disabled={branches.length <= 2}
                  onClick={() => run(() => mergeFragmentBranch(document, fragment.id, branch.id))}
                >
                  Объединить ветку {index + 1}
                </Button>
              </Group>
            </Stack>
          ))}
        </Stack>
      ) : null}
    </>
  );
}
