import type { ReactElement, ReactNode } from "react";
import { Group, NativeSelect, TextInput } from "@mantine/core";
import type { UseFormRegisterReturn } from "react-hook-form";

export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

// EndpointFormFields is the ONE copy of the method+path row both the create
// and the edit form draw. It is presentational on purpose: the two forms
// diverge in their STATE (the create form owns a kind selector and a stream
// draft, the edit form a frozen base, a conflict base and an editVersion),
// and those divergences are commented at their own call sites — folding them
// together here would bury exactly the parts a reader needs to see side by
// side. Only the markup that was visibly identical moved.
//
// The register() results arrive already bound (`register("method")`) rather
// than the register function plus a generic form type: CreateForm and
// EditForm are different arktype shapes, and a generic wrapper would need a
// cast per field to say so.
export function EndpointFormFields({
  testIdPrefix,
  methodLabel,
  methodDisabled,
  methodField,
  pathLabel,
  pathPlaceholder,
  pathError,
  pathField,
  leading,
}: {
  /** `endpoint-create` or `endpoint-edit` — the ids every test reaches by. */
  testIdPrefix: string;
  methodLabel: string;
  methodDisabled?: boolean;
  methodField: UseFormRegisterReturn;
  pathLabel: string;
  pathPlaceholder?: string;
  pathError?: string;
  pathField: UseFormRegisterReturn;
  /** The create form's «Тип» selector, which the edit form has no equivalent
   * of: a saved endpoint's kind is fixed and its row picks the editor. */
  leading?: ReactNode;
}): ReactElement {
  return (
    <Group grow align="flex-start">
      {leading}
      <NativeSelect
        label={methodLabel}
        disabled={methodDisabled}
        data-testid={`${testIdPrefix}-method`}
        {...methodField}
      >
        {HTTP_METHODS.map((method) => (
          <option key={method} value={method}>
            {method}
          </option>
        ))}
      </NativeSelect>
      <TextInput
        label={pathLabel}
        placeholder={pathPlaceholder}
        data-testid={`${testIdPrefix}-path`}
        error={pathError}
        {...pathField}
      />
    </Group>
  );
}
