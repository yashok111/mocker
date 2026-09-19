import { useState } from "react";
import type { ReactElement } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Modal,
  NativeSelect,
  Stack,
  Tabs,
  Text,
  Textarea,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { IconArrowRight, IconFileCode, IconPlus } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListApiDesignsQueryKey,
  useCreateApiDesign,
  useListApiDesigns,
} from "@/api/generated/api-designs/api-designs.ts";
import { useListWorkspaces } from "@/api/generated/workspaces/workspaces.ts";
import { describeApiFailureDetailed } from "@/api/errors";

type CreateMode = "blank" | "document" | "workspace";

export function DesignsPage(): ReactElement {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const designs = useListApiDesigns();
  const workspaces = useListWorkspaces({ all: "1" });
  const [opened, setOpened] = useState(false);
  const [mode, setMode] = useState<CreateMode>("blank");
  const [name, setName] = useState("");
  const [document, setDocument] = useState("");
  const [workspaceId, setWorkspaceId] = useState("");

  const create = useCreateApiDesign({
    mutation: {
      onSuccess: async (response) => {
        if (response.status !== 201) return;
        await queryClient.invalidateQueries({ queryKey: getListApiDesignsQueryKey() });
        setOpened(false);
        void navigate({ to: "/designs/$id", params: { id: response.data.design.id } });
      },
    },
  });
  const rows = designs.data?.status === 200 ? designs.data.data.designs : [];
  const workspaceRows = workspaces.data?.status === 200 ? workspaces.data.data : [];

  function submit(): void {
    const trimmedName = name.trim();
    if (trimmedName === "") return;
    const data =
      mode === "document"
        ? { name: trimmedName, document }
        : mode === "workspace" && workspaceId !== ""
          ? { name: trimmedName, workspaceId: Number(workspaceId) }
          : { name: trimmedName };
    create.mutate({ data });
  }

  return (
    <Stack gap="xl" data-testid="designs-page">
      <Group justify="space-between" align="flex-end">
        <div>
          <Text size="xs" c="dimmed" tt="uppercase" fw={650} lts="0.08em">
            Контракты и публикации
          </Text>
          <Title order={1}>Проектирование API</Title>
          <Text c="dimmed" mt={4}>
            Полный OpenAPI-документ, работающий черновик и стабильный опубликованный мок.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={17} />} onClick={() => setOpened(true)}>
          Новый проект
        </Button>
      </Group>

      {designs.isPending ? <Loader aria-label="Загружаем проекты" /> : null}
      {designs.isError ? (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(designs.error)}
        </Alert>
      ) : null}
      {!designs.isPending && !designs.isError && rows.length === 0 ? (
        <Stack align="center" py={72} gap="sm">
          <IconFileCode size={34} stroke={1.4} aria-hidden="true" />
          <Title order={2}>Проектов пока нет</Title>
          <Text c="dimmed" ta="center" maw={480}>
            Создайте контракт с нуля, импортируйте JSON/YAML или возьмите снимок существующего
            воркспейса.
          </Text>
        </Stack>
      ) : null}
      {rows.length > 0 ? (
        <Stack component="ul" gap={0} m={0} p={0} aria-label="Проекты API">
          {rows.map((design) => (
            <li key={design.id} style={{ listStyle: "none" }}>
              <UnstyledButton
                w="100%"
                px="md"
                py="md"
                style={{ borderBottom: "1px solid var(--mocker-border, #e0e5e1)" }}
                onClick={() => void navigate({ to: "/designs/$id", params: { id: design.id } })}
              >
                <Group justify="space-between" wrap="nowrap">
                  <div>
                    <Group gap="xs">
                      <Text fw={650}>{design.name}</Text>
                      <Badge color="gray">черновик · v{design.version}</Badge>
                      <Badge color={design.publishedRevisionId === null ? "yellow" : "teal"}>
                        {design.publishedRevisionId === null ? "не опубликован" : "опубликован"}
                      </Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={5}>
                      {design.draftUrl}
                    </Text>
                  </div>
                  <IconArrowRight size={18} aria-hidden="true" />
                </Group>
              </UnstyledButton>
            </li>
          ))}
        </Stack>
      ) : null}

      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title="Новый проект API"
        closeButtonProps={{ "aria-label": "Закрыть создание проекта" }}
      >
        <Stack gap="md">
          <TextInput
            label="Название проекта"
            required
            value={name}
            onChange={(event) => setName(event.currentTarget.value)}
          />
          <Tabs value={mode} onChange={(value) => setMode((value ?? "blank") as CreateMode)}>
            <Tabs.List grow>
              <Tabs.Tab value="blank">С нуля</Tabs.Tab>
              <Tabs.Tab value="document">Из документа</Tabs.Tab>
              <Tabs.Tab value="workspace">Из воркспейса</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="blank" pt="md">
              <Text size="sm" c="dimmed">
                Создадим минимальный OpenAPI 3.1. Его можно сразу дополнять формой или исходником.
              </Text>
            </Tabs.Panel>
            <Tabs.Panel value="document" pt="md">
              <Textarea
                label="OpenAPI JSON или YAML"
                minRows={10}
                value={document}
                onChange={(event) => setDocument(event.currentTarget.value)}
              />
            </Tabs.Panel>
            <Tabs.Panel value="workspace" pt="md">
              <NativeSelect
                label="Исходный воркспейс"
                description="У проекта будут собственные адреса моков; исходный воркспейс не изменится."
                value={workspaceId}
                onChange={(event) => setWorkspaceId(event.currentTarget.value)}
                data={[
                  { value: "", label: "Выберите воркспейс" },
                  ...workspaceRows.map((workspace) => ({
                    value: String(workspace.id),
                    label: workspace.name,
                  })),
                ]}
              />
            </Tabs.Panel>
          </Tabs>
          {create.isError ? (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(create.error)}
            </Alert>
          ) : null}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpened(false)}>
              Отмена
            </Button>
            <Button
              loading={create.isPending}
              disabled={name.trim() === "" || (mode === "workspace" && workspaceId === "")}
              onClick={submit}
            >
              Создать проект
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
