import { createCanvasId } from "./canvasId";
import type {
  CanvasDocument,
  CanvasFragment,
  CanvasFragmentBranch,
  CanvasSelection,
} from "./types";

export const FRAGMENT_LIMITS = { depth: 16, branches: 100, totalBranches: 1000 } as const;

type Range = { start: number; end: number };

function fail(message: string): never {
  throw new Error(`Некорректные блоки: ${message}`);
}

function rangeOf(
  item: Pick<CanvasFragment, "fromMessageId" | "toMessageId">,
  positions: Map<string, number>,
): Range {
  const start = positions.get(item.fromMessageId);
  const end = positions.get(item.toMessageId);
  if (start === undefined || end === undefined) fail("граница не ссылается на сообщение");
  if (start > end) fail("начало блока или ветки идёт после конца");
  return { start, end };
}

export function fragmentDepths(document: CanvasDocument): Map<string, number> {
  const byId = new Map(document.fragments.map((item) => [item.id, item]));
  const depths = new Map<string, number>();
  for (const fragment of document.fragments) {
    let current: CanvasFragment | undefined = fragment;
    const seen = new Set<string>();
    while (current) {
      if (seen.has(current.id)) fail("циклическая вложенность");
      seen.add(current.id);
      if (seen.size > FRAGMENT_LIMITS.depth) fail("глубина вложенности превышает 16");
      if (!current.parentFragmentId) break;
      current = byId.get(current.parentFragmentId);
      if (!current) fail("родительский блок не найден");
    }
    depths.set(fragment.id, seen.size - 1);
  }
  return depths;
}

// Flat ranges remain the source of message order; explicit parents remove ambiguity
// when nested frames have the same first and last message.
export function validateFragmentTree(document: CanvasDocument): void {
  if (document.formatVersion === 1) {
    if (
      document.fragments.some(
        (f) =>
          f.kind === "alt" ||
          f.parentFragmentId !== undefined ||
          f.parentBranchId !== undefined ||
          f.branches !== undefined,
      )
    ) {
      fail("ветки и вложенность требуют formatVersion 2");
    }
    return;
  }
  const positions = new Map(document.messages.map((m, i) => [m.id, i]));
  const byId = new Map(document.fragments.map((f) => [f.id, f]));
  const ranges = new Map(document.fragments.map((f) => [f.id, rangeOf(f, positions)]));
  fragmentDepths(document);
  let branchCount = 0;
  const siblings = new Map<string, Range[]>();
  for (const fragment of document.fragments) {
    const range = ranges.get(fragment.id)!;
    if (fragment.kind === "alt") {
      const branches = fragment.branches;
      if (!branches || branches.length < 2 || branches.length > FRAGMENT_LIMITS.branches)
        fail("alt должен содержать от 2 до 100 веток");
      branchCount += branches.length;
      if (branchCount > FRAGMENT_LIMITS.totalBranches) fail("слишком много веток (максимум 1000)");
      if (new Set(branches.map((b) => b.id)).size !== branches.length) fail("ID веток дублируются");
      let next = range.start;
      for (const branch of branches) {
        const current = rangeOf(branch, positions);
        if (current.start !== next) fail("ветки должны идти подряд без пробелов и пересечений");
        next = current.end + 1;
      }
      if (next !== range.end + 1) fail("ветки должны покрывать весь блок alt");
    } else if (fragment.branches !== undefined) {
      fail("ветки допустимы только для alt");
    }
    if (fragment.parentFragmentId) {
      const parent = byId.get(fragment.parentFragmentId)!;
      let parentRange = ranges.get(parent.id)!;
      if (parent.kind === "alt") {
        const branch = parent.branches?.find((b) => b.id === fragment.parentBranchId);
        if (!branch) fail("выберите ветку родительского alt");
        parentRange = rangeOf(branch, positions);
      } else if (fragment.parentBranchId !== undefined) {
        fail("ветка родителя допустима только внутри alt");
      }
      if (range.start < parentRange.start || range.end > parentRange.end)
        fail("вложенный блок выходит за границы родителя или ветки");
    } else if (fragment.parentBranchId !== undefined) {
      fail("ветка без родительского блока");
    }
    const key = JSON.stringify([
      fragment.parentFragmentId ?? null,
      fragment.parentBranchId ?? null,
    ]);
    const group = siblings.get(key) ?? [];
    group.push(range);
    siblings.set(key, group);
  }
  for (const group of siblings.values()) {
    group.sort((a, b) => a.start - b.start);
    for (let i = 1; i < group.length; i++) {
      if (group[i]!.start <= group[i - 1]!.end)
        fail("соседние блоки пересекаются; укажите вложенность");
    }
  }
}

export function upgradeFragmentDocument(document: CanvasDocument): CanvasDocument {
  if (document.formatVersion >= 2) return document;
  validateFragmentTree(document);
  const positions = new Map(document.messages.map((m, i) => [m.id, i]));
  const fragments = document.fragments.map((f) => {
    const start = positions.get(f.fromMessageId);
    const end = positions.get(f.toMessageId);
    if (start === undefined || end === undefined) fail("граница не ссылается на сообщение");
    return start <= end
      ? { ...f }
      : { ...f, fromMessageId: f.toMessageId, toMessageId: f.fromMessageId };
  });
  const ordered = fragments
    .map((fragment, index) => ({ fragment, index, ...rangeOf(fragment, positions) }))
    .sort((a, b) => a.start - b.start || b.end - a.end || a.index - b.index);
  const stack: typeof ordered = [];
  for (const current of ordered) {
    while (stack.length && stack.at(-1)!.end < current.start) stack.pop();
    const parent = stack.at(-1);
    if (parent) {
      if (current.end > parent.end) fail("старые блоки пересекаются; сначала исправьте границы");
      current.fragment.parentFragmentId = parent.fragment.id;
    }
    stack.push(current);
  }
  const result: CanvasDocument = { ...document, formatVersion: 2, fragments };
  validateFragmentTree(result);
  return result;
}

export function patchCanvasFragment(
  document: CanvasDocument,
  id: string,
  changes: Partial<CanvasFragment>,
): CanvasDocument {
  const upgraded = upgradeFragmentDocument(document);
  const result = {
    ...upgraded,
    fragments: upgraded.fragments.map((f) => (f.id === id ? { ...f, ...changes } : f)),
  };
  validateFragmentTree(result);
  return result;
}

export function setFragmentKind(
  document: CanvasDocument,
  id: string,
  kind: CanvasFragment["kind"],
): CanvasDocument {
  const upgraded = upgradeFragmentDocument(document);
  const fragment = upgraded.fragments.find((f) => f.id === id);
  if (!fragment || fragment.kind === kind) return upgraded;
  const positions = new Map(upgraded.messages.map((m, i) => [m.id, i]));
  const range = rangeOf(fragment, positions);
  let branches: CanvasFragmentBranch[] | undefined;
  if (kind === "alt") {
    if (range.start === range.end) fail("для alt нужны хотя бы два шага");
    // Find a split that does not cut any immediate child frame.
    const children = upgraded.fragments
      .filter((f) => f.parentFragmentId === id)
      .map((f) => rangeOf(f, positions));
    let split = range.start;
    while (split < range.end && children.some((r) => r.start <= split && r.end > split)) split++;
    if (split === range.end)
      fail("не удаётся разделить блок на ветки: измените границы вложенных блоков");
    branches = [
      {
        id: createCanvasId(),
        label: fragment.label,
        fromMessageId: fragment.fromMessageId,
        toMessageId: upgraded.messages[split]!.id,
      },
      {
        id: createCanvasId(),
        label: "else",
        fromMessageId: upgraded.messages[split + 1]!.id,
        toMessageId: fragment.toMessageId,
      },
    ];
  }
  const result = {
    ...upgraded,
    fragments: upgraded.fragments.map((f) => {
      if (f.id === id) {
        const { branches: _branches, ...rest } = f;
        return { ...rest, kind, ...(branches ? { branches } : {}) };
      }
      if (f.parentFragmentId !== id) return f;
      const { parentBranchId: _parentBranchId, ...rest } = f;
      const branch = branches?.find((b) => {
        const r = rangeOf(b, positions);
        const child = rangeOf(f, positions);
        return child.start >= r.start && child.end <= r.end;
      });
      return { ...rest, ...(branch ? { parentBranchId: branch.id } : {}) };
    }),
  };
  validateFragmentTree(result);
  return result;
}

export function splitFragmentBranch(
  document: CanvasDocument,
  fragmentId: string,
  branchId: string,
): CanvasDocument {
  const fragment = document.fragments.find((f) => f.id === fragmentId)!;
  const branch = fragment.branches?.find((b) => b.id === branchId);
  if (!branch) fail("ветка не найдена");
  const positions = new Map(document.messages.map((m, i) => [m.id, i]));
  const range = rangeOf(branch, positions);
  const children = document.fragments.filter(
    (f) => f.parentFragmentId === fragmentId && f.parentBranchId === branchId,
  );
  let split = range.start;
  while (
    split < range.end &&
    children.some((f) => {
      const r = rangeOf(f, positions);
      return r.start <= split && r.end > split;
    })
  )
    split++;
  if (split === range.end) fail("для новой ветки нужен свободный разделитель между шагами");
  const added = {
    ...branch,
    id: createCanvasId(),
    label: "Условие",
    fromMessageId: document.messages[split + 1]!.id,
  };
  const result = {
    ...document,
    fragments: document.fragments.map((f) => {
      if (f.id === fragmentId)
        return {
          ...f,
          branches: f.branches!.flatMap((b) =>
            b.id === branchId ? [{ ...b, toMessageId: document.messages[split]!.id }, added] : [b],
          ),
        };
      if (children.includes(f) && positions.get(f.fromMessageId)! > split)
        return { ...f, parentBranchId: added.id };
      return f;
    }),
  };
  validateFragmentTree(result);
  return result;
}

export function mergeFragmentBranch(
  document: CanvasDocument,
  fragmentId: string,
  branchId: string,
): CanvasDocument {
  const fragment = document.fragments.find((f) => f.id === fragmentId)!;
  const branches = fragment.branches;
  if (!branches || branches.length <= 2)
    fail("alt должен содержать минимум две ветки; можно сменить тип блока");
  const index = branches.findIndex((b) => b.id === branchId);
  if (index < 0) fail("ветка не найдена");
  const target = branches[index === 0 ? 1 : index - 1]!;
  const removed = branches[index]!;
  const merged = {
    ...target,
    ...(index === 0
      ? { fromMessageId: removed.fromMessageId }
      : { toMessageId: removed.toMessageId }),
  };
  const result = {
    ...document,
    fragments: document.fragments.map((f) => {
      if (f.id === fragmentId)
        return {
          ...f,
          branches: branches
            .filter((b) => b.id !== branchId)
            .map((b) => (b.id === target.id ? merged : b)),
        };
      return f.parentFragmentId === fragmentId && f.parentBranchId === branchId
        ? { ...f, parentBranchId: target.id }
        : f;
    }),
  };
  validateFragmentTree(result);
  return result;
}

export function removeCanvasFragment(document: CanvasDocument, id: string): CanvasDocument {
  document = upgradeFragmentDocument(document);
  const removed = new Set([id]);
  for (let i = 0; i < document.fragments.length; i++) {
    for (const f of document.fragments)
      if (f.parentFragmentId && removed.has(f.parentFragmentId)) removed.add(f.id);
  }
  return { ...document, fragments: document.fragments.filter((f) => !removed.has(f.id)) };
}

export function addCanvasFragment(
  document: CanvasDocument,
  selection: CanvasSelection,
  id: string,
): CanvasDocument {
  const upgraded = upgradeFragmentDocument(document);
  const positions = new Map(upgraded.messages.map((m, i) => [m.id, i]));
  const selected =
    selection?.kind === "fragment"
      ? upgraded.fragments.find((f) => f.id === selection.id)
      : undefined;
  let parent = selected;
  const start =
    selection?.kind === "message"
      ? (positions.get(selection.id) ?? 0)
      : selected
        ? positions.get(selected.fromMessageId)!
        : 0;
  if (!parent && selection?.kind === "message") {
    const depths = fragmentDepths(upgraded);
    parent = upgraded.fragments
      .filter((f) => {
        const r = rangeOf(f, positions);
        return r.start <= start && r.end >= start;
      })
      .sort((a, b) => depths.get(b.id)! - depths.get(a.id)!)[0];
  }
  const branch = parent?.branches?.find((b) => {
    const r = rangeOf(b, positions);
    return r.start <= start && r.end >= start;
  });
  let end = branch
    ? positions.get(branch.toMessageId)!
    : parent
      ? positions.get(parent.toMessageId)!
      : upgraded.messages.length - 1;
  const siblings = upgraded.fragments.filter(
    (f) => f.parentFragmentId === parent?.id && f.parentBranchId === branch?.id,
  );
  for (const sibling of siblings) {
    const r = rangeOf(sibling, positions);
    if (r.start >= start) end = Math.min(end, r.start - 1);
  }
  if (end < start || !upgraded.messages[start])
    fail("нет свободных шагов; выберите шаг или блок для вложения");
  const fragment: CanvasFragment = {
    id,
    kind: "opt",
    label: "Условие выполнения",
    fromMessageId: upgraded.messages[start]!.id,
    toMessageId: upgraded.messages[end]!.id,
    ...(parent ? { parentFragmentId: parent.id } : {}),
    ...(branch ? { parentBranchId: branch.id } : {}),
  };
  const result = { ...upgraded, fragments: [...upgraded.fragments, fragment] };
  validateFragmentTree(result);
  return result;
}

export function assertFragmentMembershipUnchanged(
  before: CanvasDocument,
  after: CanvasDocument,
): void {
  if (before.formatVersion === 1) return;
  validateFragmentTree(after);
  const positionsBefore = new Map(before.messages.map((m, i) => [m.id, i]));
  const positionsAfter = new Map(after.messages.map((m, i) => [m.id, i]));
  for (const fragment of before.fragments) {
    for (const item of [fragment, ...(fragment.branches ?? [])]) {
      const oldRange = rangeOf(item, positionsBefore);
      const newRange = rangeOf(item, positionsAfter);
      for (const message of before.messages) {
        const oldIndex = positionsBefore.get(message.id)!;
        const newIndex = positionsAfter.get(message.id)!;
        if (
          (oldIndex >= oldRange.start && oldIndex <= oldRange.end) !==
          (newIndex >= newRange.start && newIndex <= newRange.end)
        ) {
          fail(
            "перенос изменит принадлежность шага блоку или ветке; сначала измените границы блока",
          );
        }
      }
    }
  }
}
