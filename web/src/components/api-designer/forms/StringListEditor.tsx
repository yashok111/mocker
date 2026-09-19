import { TextInput } from "@mantine/core";
import { useFormFieldDraft } from "./FormDraftContext";

export function StringListEditor({
  pointer,
  value,
  onChange,
  label,
}: {
  pointer: string;
  value: unknown;
  onChange: (value: string[]) => void;
  label: string;
}) {
  const formatted = Array.isArray(value) ? value.map(String).join(", ") : "";
  const { store, draft } = useFormFieldDraft(pointer);
  const commit = () => {
    if (!draft) return;
    onChange(
      draft.source
        .split(",")
        .map((entry) => entry.trim())
        .filter(Boolean),
    );
    store.remove(pointer);
  };
  return (
    <TextInput
      label={label}
      description="Через запятую; применяются при выходе из поля"
      value={draft?.source ?? formatted}
      onChange={(event) =>
        store.set(pointer, {
          source: event.currentTarget.value,
          propertySource: draft?.propertySource ?? formatted,
        })
      }
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          commit();
        }
      }}
    />
  );
}
