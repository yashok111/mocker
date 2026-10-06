import { useState } from "react";
import { Button, Group, Text, Stack } from "@mantine/core";
export function focusWorkspaceRegion(selector: string) {
  requestAnimationFrame(() => {
    const target = document.querySelector<HTMLElement>(selector);
    let parent: HTMLElement | null = target ?? null;
    while (parent) {
      if (parent instanceof HTMLDetailsElement) parent.open = true;
      parent = parent.parentElement;
    }
    target?.scrollIntoView?.({ block: "start" });
    if (target && !target.hasAttribute("tabindex")) target.tabIndex = -1;
    target?.focus();
  });
}
export const workspaceDestinations = [
  ["Обзор и поиск", '[data-testid="backend-project-page"]'],
  ["Архитектура C4", '[data-testid="backend-architecture"] h2'],
  ["База данных", '[aria-label="База данных"] h2'],
  ["Операции и Flow", '[aria-label="Flow исходников"] h2'],
  ["События и jobs", '[aria-label="События и задачи исходников"] h2'],
  ["Потоки данных", '[aria-label="Происхождение значения"] h3'],
  ["Изменения", "#backend-change-proposals-title"],
  ["Анализ и issues", '[aria-label="Анализ источников"] h3'],
  ["Наблюдения и измерения", '[data-testid="backend-observations"] h2'],
  ["Взаимодействия", '[data-testid="backend-interactions"] h3'],
  ["Жизненный цикл", '[data-testid="backend-lifecycle"] h3'],
  ["Бизнес-события", '[data-testid="backend-business-map"] h3'],
  ["Orders replay", '[data-testid="backend-replay"] h2'],
  ["Перенос проекта", '[data-testid="backend-portable"] h2'],
  ["Материализация", '[data-testid="backend-materialization"] h2'],
] as const;
export function BackendWorkspaceNavigation() {
  const [message, setMessage] = useState("");
  return (
    <Stack gap="xs">
      <Group component="nav" aria-label="Разделы Backend Workbench" wrap="wrap">
        {workspaceDestinations.map(([label, selector]) => (
          <Button
            key={label}
            variant="default"
            onClick={() => {
              if (document.querySelector(selector)) {
                setMessage("");
                focusWorkspaceRegion(selector);
              } else {
                setMessage(
                  `${label}: выберите совместимую source revision или объект с нужными данными. Раздел не подменяется текущей ревизией.`,
                );
              }
            }}
          >
            {label}
          </Button>
        ))}
      </Group>
      <Text role="status">{message}</Text>
    </Stack>
  );
}
