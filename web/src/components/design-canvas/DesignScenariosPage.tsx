import { useState } from "react";
import type { ReactElement } from "react";
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
import { IconArrowRight, IconChartArrowsVertical, IconPlus } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { describeApiFailureDetailed } from "@/api/errors";
import { emptyCanvas } from "./canvasModel";
import {
  designScenarioKeys,
  useCreateDesignScenario,
  useListDesignScenarios,
} from "./designScenarioApi";

export function DesignScenariosPage(): ReactElement {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const query = useListDesignScenarios();
  const [createOpened, setCreateOpened] = useState(false);
  const [title, setTitle] = useState("");
  const create = useCreateDesignScenario({
    onSuccess: async (response) => {
      await queryClient.invalidateQueries({ queryKey: designScenarioKeys.all });
      setCreateOpened(false);
      void navigate({
        to: "/design-scenarios/$id" as never,
        params: { id: response.data.scenario.id } as never,
      });
    },
  });
  const scenarios = query.data?.status === 200 ? query.data.data.scenarios : [];

  function createBlank(): void {
    const trimmed = title.trim();
    if (trimmed === "") return;
    create.mutate({ document: { ...emptyCanvas(), title: trimmed }, formDrafts: {} });
  }

  return (
    <Stack gap="xl" data-testid="design-scenarios-page">
      <Group justify="space-between" align="flex-end">
        <div>
          <Text size="xs" c="dimmed" tt="uppercase" fw={650} lts="0.08em">
            Диаграммы и контракты
          </Text>
          <Title order={1}>Сценарии взаимодействия</Title>
          <Text c="dimmed" mt={4}>
            Проектируйте последовательности, связанные API и рабочие draft-моки.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={17} />} onClick={() => setCreateOpened(true)}>
          Новый сценарий
        </Button>
      </Group>

      {query.isPending ? <Loader aria-label="Загружаем сценарии" /> : null}
      {query.isError ? (
        <Alert color="red" role="alert">
          {describeApiFailureDetailed(query.error)}
        </Alert>
      ) : null}
      {!query.isPending && !query.isError && scenarios.length === 0 ? (
        <Stack align="center" py={72} gap="sm">
          <IconChartArrowsVertical size={36} stroke={1.4} aria-hidden="true" />
          <Title order={2}>Сценариев пока нет</Title>
          <Text c="dimmed">Создайте первый сценарий или перенесите локальный черновик.</Text>
        </Stack>
      ) : null}
      {scenarios.length > 0 ? (
        <Stack component="ul" gap={0} m={0} p={0} aria-label="Сценарии взаимодействия">
          {scenarios.map((scenario) => (
            <li key={scenario.id} style={{ listStyle: "none" }}>
              <UnstyledButton
                w="100%"
                px="md"
                py="md"
                aria-label={`Открыть сценарий ${scenario.name}`}
                style={{ borderBottom: "1px solid var(--mocker-border, #e0e5e1)" }}
                onClick={() =>
                  void navigate({
                    to: "/design-scenarios/$id" as never,
                    params: { id: scenario.id } as never,
                  })
                }
              >
                <Group justify="space-between" wrap="nowrap">
                  <div>
                    <Group gap="xs">
                      <Text fw={650}>{scenario.name}</Text>
                      <Badge color="gray">черновик · v{scenario.version}</Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={5}>
                      revision {scenario.draftRevisionId}
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
        opened={createOpened}
        onClose={() => setCreateOpened(false)}
        title="Новый сценарий"
        centered
      >
        <Stack gap="md">
          <TextInput
            label="Название сценария"
            required
            value={title}
            onChange={(event) => setTitle(event.currentTarget.value)}
          />
          {create.isError ? (
            <Alert color="red" role="alert">
              {describeApiFailureDetailed(create.error)}
            </Alert>
          ) : null}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpened(false)}>
              Отмена
            </Button>
            <Button loading={create.isPending} disabled={title.trim() === ""} onClick={createBlank}>
              Создать сценарий
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
