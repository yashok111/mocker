import { NativeSelect, Stack, Text, Textarea } from "@mantine/core";
export function BackendObservedImpact({
  mode,
  onMode,
  pins,
  onPins,
}: {
  mode: "none" | "pinned";
  onMode: (v: "none" | "pinned") => void;
  pins: string;
  onPins: (v: string) => void;
}) {
  return (
    <Stack>
      <NativeSelect
        label="Observed impact"
        value={mode}
        onChange={(e) => onMode(e.currentTarget.value as "none" | "pinned")}
        data={[
          { value: "none", label: "None — только структурный анализ" },
          { value: "pinned", label: "Pinned — точные сохранённые наблюдения" },
        ]}
      />
      {mode === "pinned" && (
        <>
          <Text>
            Укажите observationSetId, version, contentHash, correlationVersion, correlationHash,
            side (before/after). After допускает только source; before evidence не подтверждает
            реализацию proposal. Inferred mappings остаются inferred.
          </Text>
          <Textarea
            label="Exact observation pins JSON"
            value={pins}
            onChange={(e) => onPins(e.currentTarget.value)}
            minRows={4}
          />
        </>
      )}
    </Stack>
  );
}
