import { parseBackendSourcePin, type BackendSourcePin } from "./backendFlowReads";
import type { BackendDiagramPin, BackendDiagramViewState } from "@/api/generated/schemas";

export type BackendWorkspaceSearch = BackendSourcePin & {
  diagramId?: string;
  diagramVersion?: number;
  diagramHash?: string;
  diagramViewId?: string;
  diagramViewVersion?: number;
  diagramLevel?: "context" | "containers" | "components";
  diagramRoot?: string;
  diagramSelection?: string;
};
const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
function version(value: unknown): number {
  const n =
    typeof value === "number"
      ? value
      : typeof value === "string" && /^[1-9][0-9]*$/.test(value)
        ? Number(value)
        : 0;
  return Number.isSafeInteger(n) && n > 0 ? n : 0;
}
export function parseBackendWorkspaceSearch(
  search: Record<string, unknown>,
): BackendWorkspaceSearch {
  const pin: BackendWorkspaceSearch = parseBackendSourcePin(search);
  for (const field of [
    "diagramId",
    "diagramHash",
    "diagramViewId",
    "diagramRoot",
    "diagramSelection",
  ] as const) {
    if (search[field] !== undefined)
      pin[field] =
        typeof search[field] === "string" && search[field].length <= 256
          ? search[field]
          : "invalid";
  }
  for (const field of ["diagramVersion", "diagramViewVersion"] as const)
    if (search[field] !== undefined) pin[field] = version(search[field]);
  if (search.diagramLevel !== undefined)
    pin.diagramLevel = ["context", "containers", "components"].includes(String(search.diagramLevel))
      ? (search.diagramLevel as BackendWorkspaceSearch["diagramLevel"])
      : undefined;
  return pin;
}
export function workspacePinError(pin: BackendWorkspaceSearch): string | undefined {
  const explicit =
    pin.diagramId !== undefined ||
    pin.diagramVersion !== undefined ||
    pin.diagramHash !== undefined;
  const view = pin.diagramViewId !== undefined || pin.diagramViewVersion !== undefined;
  if ((explicit && view) || ((explicit || view) && pin.viewId))
    return "Выберите один точный mapping или сохранённый вид.";
  if (
    explicit &&
    (!uuid.test(pin.diagramId ?? "") ||
      !Number.isSafeInteger(pin.diagramVersion) ||
      !pin.diagramVersion ||
      !/^[0-9a-f]{64}$/.test(pin.diagramHash ?? ""))
  )
    return "Укажите точные ID, версию и hash mapping; округление версии запрещено.";
  if (
    view &&
    (!uuid.test(pin.diagramViewId ?? "") ||
      !Number.isSafeInteger(pin.diagramViewVersion) ||
      !pin.diagramViewVersion)
  )
    return "Укажите точные ID и версию сохранённого C4 вида.";
  if (view && (pin.diagramLevel || pin.diagramRoot || pin.diagramSelection))
    return "Уровень и выбор сохранённого вида берутся из его точной версии.";
  if (pin.diagramRoot !== undefined && !uuid.test(pin.diagramRoot)) return "Корень C4 недоступен.";
  if (pin.diagramSelection && !/^(element|link):[0-9a-f-]{36}$/.test(pin.diagramSelection))
    return "Неверный C4 selection.";
  return undefined;
}
export function diagramSearch(
  pin: BackendDiagramPin,
  state?: Pick<BackendDiagramViewState, "level" | "rootId" | "selection">,
): BackendWorkspaceSearch {
  return {
    diagramId: pin.id,
    diagramVersion: pin.version,
    diagramHash: pin.contentHash,
    diagramLevel: state?.level ?? "context",
    ...(state?.rootId ? { diagramRoot: state.rootId } : {}),
    ...(state?.selection
      ? { diagramSelection: `${state.selection.type}:${state.selection.id}` }
      : {}),
  };
}
export function explicitDiagramPin(search: BackendWorkspaceSearch): BackendDiagramPin | undefined {
  if (!search.diagramId || workspacePinError(search)) return undefined;
  return {
    id: search.diagramId,
    version: search.diagramVersion!,
    contentHash: search.diagramHash!,
  };
}

// A URL selection is navigation, not a reset of the current presentation.
export function reconcileDiagramPresentation(
  current: BackendDiagramViewState | undefined,
  selected: BackendDiagramViewState,
): BackendDiagramViewState {
  const samePin =
    current?.diagram.id === selected.diagram.id &&
    current.diagram.version === selected.diagram.version &&
    current.diagram.contentHash === selected.diagram.contentHash;
  if (!samePin || current.level !== selected.level || current.rootId !== selected.rootId)
    return selected;
  return { ...current, selection: selected.selection };
}
