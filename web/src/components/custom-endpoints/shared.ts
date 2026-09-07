import { type } from "arktype";
import type { EndpointView } from "@/api/generated/schemas";

// The pieces of the custom-endpoints screen that more than one of its four
// components needs. Split out of CustomEndpointsPage.tsx (1274 lines, one
// file) with no behaviour change: everything here is byte-for-byte what the
// page already had, and the comments came with it.

export function kindLabel(kind: EndpointView["kind"]): string | null {
  return kind === "sse" ? "SSE" : kind === "ws" ? "WebSocket" : null;
}

export const pathTemplate = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите путь" });
  }
  if (!trimmed.startsWith("/")) {
    return ctx.reject({ problem: "Путь должен начинаться с /" });
  }
  return true;
});

// statusCodeField stays a free-text string rather than a number input: a
// Mantine NumberInput has no clean way to express "empty" that survives a
// round trip through react-hook-form's register() the way an empty string
// does. `required` is the ONE way the two forms that use it differ. Create's
// status is OPTIONAL on the wire (the server defaults to 200 when it is
// omitted, per api/openapi.json's own description on
// CreateEndpointRequest.status), so an empty field there means "omit it";
// UpdateEndpointRequest.activeStatus is required — it is a full-replacement
// PUT, not create's status-defaults-to-200 POST — so an empty field there is a
// rejection. The 100..599 range was spelled out twice until A21 folded the two
// into this factory; a range that drifts apart between create and edit is a
// form that accepts what the other refuses. It lives beside pathTemplate for
// the same reason: both forms need it, and neither owns it.
export function statusCodeField(required: boolean) {
  return type("string").narrow((value, ctx) => {
    const trimmed = value.trim();
    if (trimmed === "") {
      return required ? ctx.reject({ problem: "Укажите код статуса" }) : true;
    }
    const n = Number(trimmed);
    if (!Number.isInteger(n) || n < 100 || n > 599) {
      return ctx.reject({ problem: "Код статуса — целое число от 100 до 599" });
    }
    return true;
  });
}
