import { useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  NativeSelect,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import type {
  BackendReplayProfile,
  ConnectBackendReplayProfileRequest,
} from "@/api/generated/schemas";
export function BackendTestProfiles({
  targets,
  profiles,
  selected,
  onSelect,
  onConnect,
  onRevoke,
  disabled,
}: {
  targets: { id: string; version: number; isolationId: string }[];
  profiles: BackendReplayProfile[];
  selected: string;
  onSelect: (id: string) => void;
  onConnect: (input: ConnectBackendReplayProfileRequest) => void;
  onRevoke: (profile: BackendReplayProfile) => void;
  disabled: boolean;
}) {
  const [target, setTarget] = useState("");
  const [identity, setIdentity] = useState("");
  const [consent, setConsent] = useState(false);
  const profile = profiles.find((x) => `${x.pin.id}:${x.pin.version}` === selected);
  return (
    <Stack>
      <Title order={3}>Тестовые профили</Title>
      <Text size="sm">
        Только настроенный оператором Orders fixture. Connect проверяет identity и сохраняет
        согласие. Reset выполняется только после отдельного Start.
      </Text>
      {!targets.length && <Alert>Оператор ещё не настроил тестовые targets.</Alert>}
      <NativeSelect
        label="Настроенный target"
        value={target}
        data={[
          { value: "", label: "Выберите target" },
          ...targets.map((x) => ({
            value: x.id,
            label: `${x.id} · config ${x.version} · ${x.isolationId}`,
          })),
        ]}
        onChange={(e) => {
          setTarget(e.currentTarget.value);
          setConsent(false);
        }}
      />
      <TextInput
        label="Ожидаемый identity hash от оператора"
        value={identity}
        onChange={(e) => {
          setIdentity(e.currentTarget.value);
          setConsent(false);
        }}
      />
      <Checkbox
        label="Разрешаю сброс данных этого изолированного fixture при явном запуске replay"
        checked={consent}
        onChange={(e) => setConsent(e.currentTarget.checked)}
      />
      <Button
        disabled={disabled || !target || !/^[a-f0-9]{64}$/.test(identity) || !consent}
        onClick={() =>
          onConnect({
            configuredTargetId: target,
            expectedIdentityHash: identity,
            allowReset: true,
            idempotencyKey: crypto.randomUUID(),
          })
        }
      >
        Connect и сохранить согласие
      </Button>
      <NativeSelect
        label="Точная версия профиля"
        value={selected}
        data={[
          { value: "", label: "Выберите профиль" },
          ...profiles.map((x) => ({
            value: `${x.pin.id}:${x.pin.version}`,
            label: `${x.targetId} · ${x.identity.variant} · v${x.pin.version} · ${x.pin.id}`,
          })),
        ]}
        onChange={(e) => onSelect(e.currentTarget.value)}
      />
      {profile && (
        <>
          <Text size="xs" style={{ overflowWrap: "anywhere" }}>
            Identity: {profile.identityHash} · build: {profile.identity.buildHash} · source:{" "}
            {profile.identity.sourceTreeHash}
          </Text>
          <Button color="orange" disabled={disabled} onClick={() => onRevoke(profile)}>
            Отозвать согласие профиля
          </Button>
        </>
      )}
    </Stack>
  );
}
