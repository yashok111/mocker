import type { ReactElement } from "react";
import type { Variant } from "@/api/generated/schemas";
import { VariantEditor } from "../VariantEditor";

// StatusPanel is the per-status mount of VariantEditor.tsx — the one editor
// of a response variant since A21 step 5. It keeps this screen's test ids
// (`operation-status-<field>-<selector>`, `operation-when-<field>-<selector>-<i>`)
// and reports the variant's validity up to the «Сохранить» button through
// onBodyErrorChange, as before; everything about the variant itself —
// producer, body, file, function, headers, conditions — lives in the shared
// editor, so a field added there reaches both screens or neither.
export function StatusPanel({
  workspaceId,
  selector,
  variant,
  updateVariant,
  onBodyErrorChange,
  hasSchema,
}: {
  workspaceId: number;
  selector: string;
  variant: Variant | undefined;
  updateVariant: (updater: (v: Variant) => Variant) => void;
  onBodyErrorChange: (hasError: boolean) => void;
  /** Whether the spec declares this status (a code added by hand has no
   * schema to generate from). */
  hasSchema: boolean;
}): ReactElement {
  return (
    <VariantEditor
      workspaceId={workspaceId}
      variant={variant}
      updateVariant={updateVariant}
      onErrorChange={onBodyErrorChange}
      testId={(name) => `operation-status-${name}-${selector}`}
      whenTestId={(name, index) => `operation-when-${name}-${selector}-${index}`}
      hasSchema={hasSchema}
      // A spec operation's override headers are layered only under a pinned
      // variant (respond.go); a file is pinned on the wire.
      headersAppliedOn={["pinned", "file"]}
    />
  );
}
