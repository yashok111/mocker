import { BackendPortable } from "./BackendPortable";
import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Modal,
  Stack,
  Text,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { IconArrowRight, IconDatabase, IconPlus } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  getListBackendProjectsQueryKey,
  useCreateBackendProject,
  useGetBackendCapabilities,
  useListBackendProjects,
} from "@/api/generated/backend-projects/backend-projects";
import type { CreateBackendProjectRequest } from "@/api/generated/schemas";
import { ApiFailure } from "@/api/client";
import { describeApiFailureDetailed } from "@/api/errors";

export function BackendProjectsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [cursors, setCursors] = useState<string[]>([""]);
  const cursor = cursors.at(-1) ?? "";
  const projects = useListBackendProjects(cursor ? { cursor } : undefined);
  const capabilities = useGetBackendCapabilities({ query: { staleTime: 60_000 } });
  const limits = capabilities.data?.status === 200 ? capabilities.data.data.limits : undefined;
  const [opened, setOpened] = useState(false);
  const [name, setName] = useState("");
  const [attempt, setAttempt] = useState<CreateBackendProjectRequest | null>(null);
  const create = useCreateBackendProject({
    mutation: {
      retry: false,
      onSuccess: (response) => {
        if (response.status !== 201) return;
        setAttempt(null);
        setOpened(false);
        void queryClient.invalidateQueries({ queryKey: getListBackendProjectsQueryKey() });
        void navigate({
          to: "/backend-projects/$projectId",
          params: { projectId: response.data.id },
        });
      },
      onError: (error) => {
        // A definitive invalid request can be edited; uncertain delivery retains its receipt key.
        if (
          error instanceof ApiFailure &&
          error.status >= 400 &&
          error.status < 500 &&
          error.status !== 408 &&
          error.status !== 429
        )
          setAttempt(null);
      },
    },
  });
  const page = projects.data?.status === 200 ? projects.data.data : undefined;
  const nameTooLong = limits !== undefined && Array.from(name.trim()).length > limits.maxNameLength;

  function submit() {
    if (create.isPending || !limits || nameTooLong || (!attempt && name.trim() === "")) return;
    const input = attempt ?? { name: name.trim(), idempotencyKey: crypto.randomUUID() };
    setAttempt(input);
    create.mutate({ data: input });
  }

  return (
    <Stack gap="xl" data-testid="backend-projects-page">
      <Group justify="space-between" align="flex-end">
        <div>
          <Text size="xs" c="dimmed" tt="uppercase" fw={650} lts="0.08em">
            Модель приложения
          </Text>
          <Title order={1}>Бэкенд-проекты</Title>
          <Text c="dimmed" mt={4}>
            Проекты и ревизии модели вашего бэкенда.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={17} />} onClick={() => setOpened(true)}>
          Новый бэкенд-проект
        </Button>
      </Group>
      <details>
        <summary>Импортировать portable-пакет в новый проект</summary>
        <BackendPortable />
      </details>
      {projects.isPending && <Loader aria-label="Загружаем бэкенд-проекты" />}
      {capabilities.isError && (
        <Alert color="red" role="alert">
          Не удалось загрузить ограничения сервера. Создание временно недоступно.
          <Button mt="sm" variant="light" onClick={() => void capabilities.refetch()}>
            Повторить загрузку ограничений
          </Button>
        </Alert>
      )}
      {projects.isError && (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(projects.error)}
          <Button mt="sm" variant="light" onClick={() => void projects.refetch()}>
            Повторить загрузку
          </Button>
        </Alert>
      )}
      {page?.items.length === 0 && !projects.isError && (
        <Stack align="center" py={64} gap="sm">
          <IconDatabase size={34} stroke={1.4} aria-hidden="true" />
          <Title order={2}>
            {cursor ? "На этой странице нет проектов" : "Бэкенд-проектов пока нет"}
          </Title>
          <Text c="dimmed" ta="center" maw={520}>
            Создайте проект для будущей модели приложения. До импорта исходников её покрытие будет
            неизвестно.
          </Text>
        </Stack>
      )}
      {!!page?.items.length && (
        <Stack component="ul" m={0} p={0} gap={0} aria-label="Бэкенд-проекты">
          {page.items.map((project) => (
            <li key={project.id} style={{ listStyle: "none" }}>
              <UnstyledButton
                w="100%"
                py="md"
                px="md"
                style={{ borderBottom: "1px solid var(--mocker-border, #e0e5e1)" }}
                onClick={() =>
                  void navigate({
                    to: "/backend-projects/$projectId",
                    params: { projectId: project.id },
                  })
                }
              >
                <Group justify="space-between" wrap="nowrap">
                  <div style={{ minWidth: 0 }}>
                    <Text fw={650} style={{ overflowWrap: "anywhere" }}>
                      {project.name}
                    </Text>
                    <Text size="xs" c="dimmed" mt={4}>
                      {new Date(project.updatedAt).toLocaleString("ru-RU")}
                    </Text>
                  </div>
                  <Group gap="xs" wrap="nowrap">
                    <Badge color="gray">v{project.version}</Badge>
                    <IconArrowRight size={18} aria-hidden="true" />
                  </Group>
                </Group>
              </UnstyledButton>
            </li>
          ))}
        </Stack>
      )}
      {(cursors.length > 1 || page?.nextCursor) && (
        <Group justify="space-between">
          <Button
            variant="default"
            disabled={cursors.length === 1 || projects.isFetching}
            onClick={() => setCursors((previous) => previous.slice(0, -1))}
          >
            Предыдущая страница
          </Button>
          <Button
            variant="default"
            disabled={!page?.nextCursor || projects.isFetching}
            onClick={() => {
              if (page?.nextCursor) setCursors((previous) => [...previous, page.nextCursor]);
            }}
          >
            Следующая страница
          </Button>
        </Group>
      )}
      <Modal
        opened={opened}
        onClose={() => setOpened(false)}
        title="Новый бэкенд-проект"
        closeOnClickOutside={!create.isPending}
        closeOnEscape={!create.isPending}
        closeButtonProps={{ "aria-label": "Закрыть создание проекта", disabled: create.isPending }}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <Stack>
            <TextInput
              label="Название проекта"
              value={name}
              onChange={(event) => {
                setName(event.currentTarget.value);
                create.reset();
              }}
              disabled={attempt !== null}
              required
              data-autofocus
              error={nameTooLong ? `Не больше ${limits?.maxNameLength} символов` : undefined}
            />
            {create.isError && (
              <Alert color="red" role="alert">
                {describeApiFailureDetailed(create.error)}
              </Alert>
            )}
            {attempt && create.isError && (
              <Text size="sm" c="dimmed">
                Результат запроса неизвестен. Повторите его, чтобы получить результат без создания
                дубликата.
              </Text>
            )}
            <Button
              type="submit"
              loading={create.isPending}
              disabled={!limits || nameTooLong || (!attempt && name.trim() === "")}
            >
              {attempt && create.isError ? "Повторить запрос" : "Создать проект"}
            </Button>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
}
