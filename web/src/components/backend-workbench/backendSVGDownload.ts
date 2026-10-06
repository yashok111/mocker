import { exportBackendViewSVG } from "@/api/generated/backend-projects/backend-projects";
import type { BackendSVGPin } from "./BackendViewSVGExport";

export async function downloadBackendSVG(pin: BackendSVGPin): Promise<void> {
  if (!Number.isSafeInteger(pin.viewVersion) || pin.viewVersion < 1)
    throw new Error("Выберите точную версию вида");
  const response = await exportBackendViewSVG(pin.projectId, pin.viewId, pin.viewVersion);
  if (response.status !== 200 || !(response.data instanceof Blob))
    throw new Error("Не удалось получить SVG-файл");
  const url = URL.createObjectURL(response.data);
  const link = document.createElement("a");
  link.href = url;
  link.download = `backend-view-${pin.viewId}-v${pin.viewVersion}.svg`;
  document.body.append(link);
  try {
    link.click();
  } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
}
