import { Badge, Button, Code, Stack, Text, TextInput } from "@mantine/core";
import { useState } from "react";
import type { ObservationCorrelationSnapshot, ObservationOverride } from "@/api/generated/schemas";
export function BackendCorrelation({
  snapshot,
  onOverride,
}: {
  snapshot: ObservationCorrelationSnapshot;
  onOverride: (value: ObservationOverride) => void;
}) {
  const [reason, setReason] = useState("");
  return (
    <Stack>
      <Badge>{snapshot.sourceCompatible ? "Source compatible" : "Неподтверждённая сборка"}</Badge>
      <Text>
        Observed correlation v{snapshot.version} · {snapshot.contentHash}
      </Text>
      <Code block>{JSON.stringify(snapshot.input, null, 2)}</Code>
      <Text>{snapshot.gaps.join("; ")}</Text>
      <TextInput
        label="Причина ручного сопоставления"
        value={reason}
        onChange={(e) => setReason(e.currentTarget.value)}
      />
      {snapshot.rows.map((row) => (
        <Stack key={row.recordId} gap="xs">
          <Text>
            {row.recordId}: {row.outcome} ({row.method})
          </Text>
          <Text>{row.reasons.join("; ")}</Text>
          {row.candidates.map((ref, i) => (
            <Button
              key={i}
              variant="default"
              disabled={!reason.trim()}
              onClick={() => onOverride({ recordId: row.recordId, ref, reason })}
            >
              Выбрать {ref.kind}: {ref.kind === "record" ? ref.id : ref.rowId} — inferred/manual
            </Button>
          ))}
        </Stack>
      ))}
      {snapshot.diagramRows.map((row) => (
        <Text key={row.recordId}>
          {row.recordId}: {row.basis}; {row.selectors.map((s) => s.id).join(", ")};{" "}
          {row.gaps.join("; ")}
        </Text>
      ))}
    </Stack>
  );
}
