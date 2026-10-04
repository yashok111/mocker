import { useEffect, useRef, useState } from "react";
import { Alert, Stack, Text, TextInput } from "@mantine/core";
import { readSourceJson } from "./backendSourceInputs";

export function BackendSourceFileInput<T>({
  label,
  disabled,
  limit,
  parse,
  onValue,
  onBusy,
}: {
  label: string;
  disabled?: boolean;
  limit?: number;
  parse: (value: unknown) => T;
  onValue: (value: T | undefined) => void;
  onBusy?: (busy: boolean) => void;
}) {
  const generation = useRef(0);
  const busyCallback = useRef(onBusy);
  useEffect(() => {
    busyCallback.current = onBusy;
  }, [onBusy]);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(
    () => () => {
      generation.current++;
      busyCallback.current?.(false);
    },
    [],
  );
  async function load(file: File | undefined) {
    const token = ++generation.current;
    onValue(undefined);
    setError("");
    setName(file?.name ?? "");
    if (!file) {
      setBusy(false);
      onBusy?.(false);
      return;
    }
    setBusy(true);
    onBusy?.(true);
    try {
      const value = parse(await readSourceJson(file, limit));
      if (token === generation.current) onValue(value);
    } catch (error) {
      if (token === generation.current)
        setError(error instanceof Error ? error.message : "Не удалось прочитать JSON.");
    } finally {
      if (token === generation.current) {
        setBusy(false);
        onBusy?.(false);
      }
    }
  }
  return (
    <Stack gap="xs">
      <TextInput
        type="file"
        accept="application/json,.json"
        label={label}
        disabled={disabled}
        onChange={(event) => void load(event.currentTarget.files?.[0])}
        styles={{ input: { maxWidth: "100%" } }}
      />
      {name && (
        <Text size="xs" style={{ overflowWrap: "anywhere" }}>
          {name}
          {busy ? " · чтение…" : ""}
        </Text>
      )}
      {error && (
        <Alert color="red" role="alert">
          {error}
        </Alert>
      )}
    </Stack>
  );
}
