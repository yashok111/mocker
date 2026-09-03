import { type } from "arktype";
import type { ArkErrors } from "arktype";
import type { FieldErrors, FieldValues, Resolver } from "react-hook-form";

// arktypeResolver adapts an arktype validator to react-hook-form's Resolver
// contract, so a form declares its rules once as a type and gets both the
// parsed value and per-field messages out of the same declaration.
//
// Written here rather than pulled from @hookform/resolvers: that package
// carries an adapter for every validation library there is, and this is
// thirty lines against one of them.
//
// The parameter is typed as the CALL signature rather than as `Type<T>` on
// purpose: an arktype schema distinguishes its input type from its output
// type, and naming the type constructor would make TypeScript infer the
// former — leaving every field typed `unknown` and the resolver unassignable
// to the form it was written for.
export function arktypeResolver<TFieldValues extends FieldValues>(
  schema: (data: unknown) => TFieldValues | ArkErrors,
): Resolver<TFieldValues, unknown, TFieldValues> {
  return (values) => {
    const out = schema(values);

    if (out instanceof type.errors) {
      const errors: FieldErrors<TFieldValues> = {};
      for (const issue of out) {
        // issue.path is arktype's own path into the value; joined with "."
        // it is exactly the dotted field name react-hook-form registers
        // nested fields under. An issue with an empty path belongs to the
        // whole form and has no field to attach to, so it is dropped rather
        // than reported against an arbitrary input.
        const name = issue.path.join(".");
        if (name === "") {
          continue;
        }
        // First message wins: react-hook-form shows one message per field,
        // and the first arktype produced is the one about the outermost
        // failing rule.
        if (name in errors) {
          continue;
        }
        // issue.problem, not issue.message: the latter prefixes the field's
        // own path ("name Введите имя"), which reads as noise under an input
        // that is already labelled.
        Object.assign(errors, { [name]: { type: "validation", message: issue.problem } });
      }
      return { values: {}, errors };
    }

    return { values: out, errors: {} };
  };
}
