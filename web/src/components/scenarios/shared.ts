import { type } from "arktype";
import { ApiFailure } from "@/api/client";
import { describeApiFailure, describeApiFailureDetailed } from "@/api/errors";

// The vocabulary the scenarios screen's four forms and its row list share.
// Split out of ScenariosPage.tsx (813 lines, one file) with no behaviour
// change; each comment travelled with the thing it explains.

export const nameField = type("string").narrow((value, ctx) => {
  const trimmed = value.trim();
  if (trimmed === "") {
    return ctx.reject({ problem: "Укажите имя сценария" });
  }
  return true;
});

export const createForm = type({ name: nameField });
export type CreateForm = typeof createForm.infer;

export const EMPTY_FORM: CreateForm = { name: "" };

// describeMutationFailure picks between the two Russian renderers the same
// way the create form above already does: a 409 here names either the
// TAKEN name (both routes share the create path's ErrDuplicateName → 409)
// or, for clone, a source scenario that vanished from under the request —
// either way the server's own sentence is the actionable content, not
// incidental detail. Anything else (400 on a blank/invalid name that somehow
// reached the wire, 404 on a source or a scenario deleted from another tab)
// gets the generic summary instead, same as every other screen's fallback.
export function describeMutationFailure(err: unknown): string {
  return err instanceof ApiFailure && err.status === 409
    ? describeApiFailureDetailed(err)
    : describeApiFailure(err);
}
