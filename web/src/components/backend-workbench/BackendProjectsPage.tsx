import { useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Group,
  Loader,
  Stack,
  Text,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { IconArrowRight, IconDatabase } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useListBackendProjects } from "@/api/generated/backend-projects/backend-projects";
import { describeApiFailureDetailed } from "@/api/errors";
export function BackendProjectsPage() {
  const navigate = useNavigate();
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors.at(-1) ?? "";
  const projects = useListBackendProjects(cursor ? { cursor } : undefined);
  const page = projects.data?.status === 200 ? projects.data.data : undefined;
  return (
    <Stack gap="xl" data-testid="backend-projects-page">
      <div>
        <Text size="xs" c="dimmed" tt="uppercase" fw={650} lts="0.08em">
          Карта приложения
        </Text>
        <Title order={1}>Бэкенд-проекты</Title>
        <Text c="dimmed" mt={4}>
          Архитектура, сценарии и данные ваших систем.
        </Text>
      </div>
      {projects.isPending && <Loader aria-label="Загружаем бэкенд-проекты" />}
      {projects.isError && (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(projects.error)}
          <Button variant="subtle" onClick={() => void projects.refetch()}>
            Повторить загрузку
          </Button>
        </Alert>
      )}
      {page?.items.length === 0 && !projects.isError && (
        <Stack align="center" py={64}>
          <IconDatabase size={34} stroke={1.4} />
          <Title order={2}>
            {cursor ? "На этой странице нет проектов" : "Бэкенд-проектов пока нет"}
          </Title>
          <Text c="dimmed" ta="center" maw={520}>
            Попросите агента создать проект и импортировать исходники через MCP. Здесь появится
            карта системы.
          </Text>
        </Stack>
      )}
      {!!page?.items.length && (
        <Stack component="ul" m={0} p={0} gap={0} aria-label="Бэкенд-проекты">
          {page.items.map((project) => (
            <li key={project.id} style={{ listStyle: "none" }}>
              <UnstyledButton
                w="100%"
                py="lg"
                px="md"
                style={{ borderBottom: "1px solid var(--mocker-border,#e0e5e1)" }}
                onClick={() =>
                  void navigate({
                    to: "/backend-projects/$projectId",
                    params: { projectId: project.id },
                  })
                }
              >
                <Group justify="space-between" wrap="nowrap">
                  <div style={{ minWidth: 0 }}>
                    <Text fw={650}>{project.name}</Text>
                    <Text size="xs" c="dimmed" mt={4}>
                      {new Date(project.updatedAt).toLocaleString("ru-RU")}
                    </Text>
                  </div>
                  <Group gap="xs">
                    <Badge color="gray" variant="light">
                      Открыть карту
                    </Badge>
                    <IconArrowRight size={18} />
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
            onClick={() => setCursors((p) => p.slice(0, -1))}
          >
            Предыдущая страница
          </Button>
          <Button
            variant="default"
            disabled={!page?.nextCursor || projects.isFetching}
            onClick={() => {
              if (page?.nextCursor) setCursors((p) => [...p, page.nextCursor]);
            }}
          >
            Следующая страница
          </Button>
        </Group>
      )}
    </Stack>
  );
}
